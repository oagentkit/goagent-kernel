// k8sjobtransport.go —— K8s Job 的传输管道：apiserver 请求骨架、状态码归一与路径。
//
// 拆分只为满足单文件 400 行阈值：载荷构造在 k8sjobwire.go，语义在 k8sjob.go。
package containerbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/backend"
)

type k8sResponse struct {
	status int
	body   []byte
}

func (k *K8sJob) call(ctx context.Context, method, path string, body any, q url.Values) (k8sResponse, error) {
	u := k.endpoint + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return k8sResponse{}, errors.Wrap(ErrAPICall, "containerbackend: 编码 apiserver 请求失败: "+err.Error())
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return k8sResponse{}, errors.Wrap(ErrAPICall, "containerbackend: 构造 apiserver 请求失败: "+err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token, err := k.bearerToken()
	if err != nil {
		return k8sResponse{}, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return k8sResponse{}, errors.Wrap(ErrAPICall, "containerbackend: apiserver 不可达: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return k8sResponse{}, errors.Wrap(ErrAPICall, "containerbackend: 读取 apiserver 应答失败: "+err.Error())
	}
	return k8sResponse{status: resp.StatusCode, body: raw}, nil
}

// bearerToken 每请求重读 token（轮换生效）；缺省文件不存在 = 无认证。
func (k *K8sJob) bearerToken() (string, error) {
	if k.tokenFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(k.tokenFile)
	if err != nil {
		if os.IsNotExist(err) && !k.tokenExplicit {
			return "", nil
		}
		return "", errors.Wrap(ErrAPICall, "containerbackend: 读取 service account token 失败: "+err.Error())
	}
	return strings.TrimSpace(string(b)), nil
}

// k8sAPIError 非 2xx 即基础设施故障（错误体带 apiserver 自己的解释）。
func k8sAPIError(op string, resp k8sResponse) error {
	if resp.status >= 200 && resp.status < 300 {
		return nil
	}
	msg := strings.TrimSpace(string(resp.body))
	if len(msg) > 512 {
		msg = msg[:512]
	}
	return errors.Wrap(ErrAPICall, fmt.Sprintf("containerbackend: %s 返回 %d: %s", op, resp.status, msg))
}

func (k *K8sJob) createJob(ctx context.Context, name string, j backend.Job) error {
	resp, err := k.call(ctx, http.MethodPost, k.jobsPath(""), buildK8sJob(name, j, k), nil)
	if err != nil {
		return err
	}
	return k8sAPIError("创建 Job", resp)
}

func (k *K8sJob) getJob(ctx context.Context, name string) (k8sJobResponse, bool, error) {
	resp, err := k.call(ctx, http.MethodGet, k.jobsPath(name), nil, nil)
	if err != nil {
		return k8sJobResponse{}, false, err
	}
	if resp.status == http.StatusNotFound {
		return k8sJobResponse{}, false, nil
	}
	if err := k8sAPIError("查询 Job", resp); err != nil {
		return k8sJobResponse{}, false, err
	}
	var out k8sJobResponse
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return k8sJobResponse{}, false, errors.Wrap(ErrAPICall, "containerbackend: 解析 Job 应答失败: "+err.Error())
	}
	return out, true, nil
}

func (k *K8sJob) listPods(ctx context.Context, name string) ([]k8sPod, error) {
	q := url.Values{"labelSelector": {k8sJobLabel + "=" + name}}
	resp, err := k.call(ctx, http.MethodGet, k.podsPath(), nil, q)
	if err != nil {
		return nil, err
	}
	if err := k8sAPIError("查询 Pod", resp); err != nil {
		return nil, err
	}
	var out k8sPodList
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return nil, errors.Wrap(ErrAPICall, "containerbackend: 解析 Pod 应答失败: "+err.Error())
	}
	return out.Items, nil
}

func (k *K8sJob) podLogs(ctx context.Context, pod string) (string, error) {
	q := url.Values{"container": {k8sJobContainerName}}
	resp, err := k.call(ctx, http.MethodGet, k.podsPath()+"/"+url.PathEscape(pod)+"/log", nil, q)
	if err != nil {
		return "", err
	}
	if err := k8sAPIError("读取 Pod 日志", resp); err != nil {
		return "", err
	}
	return string(resp.body), nil
}

func (k *K8sJob) createNetworkPolicy(ctx context.Context, name string) error {
	resp, err := k.call(ctx, http.MethodPost, k.networkPoliciesPath(""), buildK8sNetworkPolicy(name, k), nil)
	if err != nil {
		return err
	}
	return k8sAPIError("创建 NetworkPolicy", resp)
}

func (k *K8sJob) deleteJob(ctx context.Context, name string) error {
	q := url.Values{"propagationPolicy": {"Background"}}
	resp, err := k.call(ctx, http.MethodDelete, k.jobsPath(name), nil, q)
	if err != nil {
		return err
	}
	if resp.status == http.StatusNotFound {
		return nil // 已被 TTL / 控制面清掉：清理是幂等的
	}
	return k8sAPIError("删除 Job", resp)
}

func (k *K8sJob) deleteNetworkPolicy(ctx context.Context, name string) error {
	resp, err := k.call(ctx, http.MethodDelete, k.networkPoliciesPath(name), nil, nil)
	if err != nil {
		return err
	}
	if resp.status == http.StatusNotFound {
		return nil
	}
	return k8sAPIError("删除 NetworkPolicy", resp)
}

func (k *K8sJob) jobsPath(name string) string {
	p := "/apis/batch/v1/namespaces/" + url.PathEscape(k.namespace) + "/jobs"
	if name != "" {
		p += "/" + url.PathEscape(name)
	}
	return p
}

func (k *K8sJob) networkPoliciesPath(name string) string {
	p := "/apis/networking.k8s.io/v1/namespaces/" + url.PathEscape(k.namespace) + "/networkpolicies"
	if name != "" {
		p += "/" + url.PathEscape(name)
	}
	return p
}

func (k *K8sJob) podsPath() string {
	return "/api/v1/namespaces/" + url.PathEscape(k.namespace) + "/pods"
}
