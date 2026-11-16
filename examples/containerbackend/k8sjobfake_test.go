// k8sjobfake_test.go —— 假 kube-apiserver：与 docker.go 的假引擎同款手法，
// 用 httptest 记录请求体与调用次数，按脚本回 Job 状态。
package containerbackend

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const (
	testK8sNamespace = "team-a"
	testK8sSA        = "runner-sa"
	testK8sImage     = "registry.example/app@sha256:deadbeef"
)

// fakeKube 只实现 K8sJob 用到的路径；未注册路径由 mux 回 404。
type fakeKube struct {
	mu sync.Mutex

	jobRaw     [][]byte
	npRaw      [][]byte
	jobDeletes []string
	npDeletes  []string
	getCalls   int

	npCreateStatus  int
	jobCreateStatus int
	jobGetStatus    int // 非 0 ⇒ 每次 GET 都回该状态码（404 用例）
	podsStatus      int
	logStatus       int

	statuses []k8sJobStatus // 按 GET 次序取；耗尽后重复最后一项
	pods     []k8sPod
	logs     map[string]string
	deleting bool
}

func (f *fakeKube) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /apis/networking.k8s.io/v1/namespaces/{ns}/networkpolicies", func(w http.ResponseWriter, r *http.Request) {
		raw := readBody(r)
		f.mu.Lock()
		f.npRaw = append(f.npRaw, raw)
		st := f.npCreateStatus
		f.mu.Unlock()
		if st != 0 {
			writeStatus(w, st, "networkpolicy rejected")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"metadata": map[string]any{"name": bodyName(raw)}})
	})
	mux.HandleFunc("POST /apis/batch/v1/namespaces/{ns}/jobs", func(w http.ResponseWriter, r *http.Request) {
		raw := readBody(r)
		f.mu.Lock()
		f.jobRaw = append(f.jobRaw, raw)
		st := f.jobCreateStatus
		f.mu.Unlock()
		if st != 0 {
			writeStatus(w, st, "job rejected")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"metadata": map[string]any{"name": bodyName(raw), "uid": "uid-1"}})
	})
	mux.HandleFunc("GET /apis/batch/v1/namespaces/{ns}/jobs/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.getCalls++
		idx := f.getCalls - 1
		var st k8sJobStatus
		if len(f.statuses) > 0 {
			if idx >= len(f.statuses) {
				idx = len(f.statuses) - 1
			}
			st = f.statuses[idx]
		}
		code, deleting := f.jobGetStatus, f.deleting
		f.mu.Unlock()
		if code != 0 {
			writeStatus(w, code, "job not found")
			return
		}
		meta := map[string]any{"name": r.PathValue("name"), "uid": "uid-1"}
		if deleting {
			meta["deletionTimestamp"] = "2026-01-01T00:00:00Z"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"apiVersion": "batch/v1", "kind": "Job", "metadata": meta, "status": st,
		})
	})
	mux.HandleFunc("DELETE /apis/batch/v1/namespaces/{ns}/jobs/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.jobDeletes = append(f.jobDeletes, r.PathValue("name"))
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"status": "Success"})
	})
	mux.HandleFunc("DELETE /apis/networking.k8s.io/v1/namespaces/{ns}/networkpolicies/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.npDeletes = append(f.npDeletes, r.PathValue("name"))
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"status": "Success"})
	})
	mux.HandleFunc("GET /api/v1/namespaces/{ns}/pods", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		pods := append([]k8sPod(nil), f.pods...)
		code := f.podsStatus
		f.mu.Unlock()
		if code != 0 {
			writeStatus(w, code, "pods rejected")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": pods})
	})
	mux.HandleFunc("GET /api/v1/namespaces/{ns}/pods/{pod}/log", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		text, ok := f.logs[r.PathValue("pod")]
		code := f.logStatus
		f.mu.Unlock()
		if code != 0 {
			writeStatus(w, code, "logs rejected")
			return
		}
		if !ok {
			text = "log:" + r.PathValue("pod")
		}
		_, _ = w.Write([]byte(text))
	})
	return mux
}

type fakeKubeSnapshot struct {
	jobRaw     [][]byte
	npRaw      [][]byte
	jobDeletes []string
	npDeletes  []string
	getCalls   int
}

func (f *fakeKube) snapshot() fakeKubeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeKubeSnapshot{
		jobRaw:     append([][]byte(nil), f.jobRaw...),
		npRaw:      append([][]byte(nil), f.npRaw...),
		jobDeletes: append([]string(nil), f.jobDeletes...),
		npDeletes:  append([]string(nil), f.npDeletes...),
		getCalls:   f.getCalls,
	}
}

func newFakeK8sJob(t *testing.T, f *fakeKube, extra ...K8sJobOption) *K8sJob {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	opts := []K8sJobOption{
		WithK8sEndpoint(srv.URL),
		WithK8sNamespace(testK8sNamespace),
		WithK8sServiceAccount(testK8sSA),
		WithK8sPollInterval(2 * time.Millisecond),
		WithK8sTTLSeconds(45),
		WithK8sMountPolicy(MountPolicy{AllowedSources: []string{"/src"}}),
	}
	return NewK8sJob(append(opts, extra...)...)
}

func readBody(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}

func bodyName(raw []byte) string {
	var meta struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &meta)
	return meta.Metadata.Name
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"message": msg})
}
