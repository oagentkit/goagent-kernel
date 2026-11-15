package dispatch

import (
	"strings"
	"testing"

	"github.com/oagentkit/guardx/toolfail"
	"github.com/oekit/pkg/errors"
)

// 三档分类词表的契约判据（issue #169）：三档各一组 + 状态码词边界负例 +
// Disposition 优先于文本。夹具全部用合成错误文案（不带任何宿主领域词面）。

// TestClassifyThreeClasses 三档分类各一组（第二级文本兜底层）。
func TestClassifyThreeClasses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Class
	}{
		{
			name: "瞬态：连接类关键词",
			err:  errors.New("upstream connect error: connection refused"),
			want: ClassTransient,
		},
		{
			name: "瞬态：状态码 503（词边界命中）",
			err:  errors.New("bad gateway: upstream returned status=503"),
			want: ClassTransient,
		},
		{
			name: "终态：状态码 403（词边界命中）",
			err:  errors.New("request rejected, http status 403"),
			want: ClassFatal,
		},
		{
			name: "终态：凭证类关键词",
			err:  errors.New("identity check failed: unauthorized"),
			want: ClassFatal,
		},
		{
			name: "不确定：既无瞬态词也无终态词",
			err:  errors.New("payload mismatch on field q"),
			want: ClassOther,
		},
		{
			name: "无失败：保守按不确定",
			err:  nil,
			want: ClassOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Fatalf("Classify() = %q, want %q（err=%v）", got, tc.want, tc.err)
			}
		})
	}
}

// TestClassifyStatusUsesWordBoundary 状态码判定必须带 \b 词边界（纪律②）。
//
// 负例是真实事故形态：错误文本里拼着「省略中段 4032 字节（全长 9120）」这类
// 预算截断提示——裸 Contains("403") 会把 4032 判成 fatal，瞬态跨域兜底被
// 错误抑制；行号/字节数同理。正例钉「真状态码仍要命中」（只测负例等于没测边界）。
func TestClassifyStatusUsesWordBoundary(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want Class
	}{
		{
			name: "4032 字节不得判成 403（裸 Contains 的事故形态）",
			msg:  "输出超预算，省略中段 4032 字节（全长 9120）",
			want: ClassOther,
		},
		{
			name: "4012 端口号不得判成 401",
			msg:  "dial tcp: connect to 127.0.0.1:4012 failed",
			want: ClassTransient, // connect 关键词命中，状态码不参与
		},
		{
			name: "独立的 403 仍要判终态",
			msg:  "policy check failed with 403",
			want: ClassFatal,
		},
		{
			name: "独立的 504 仍要判瞬态",
			msg:  "gateway timeout: 504",
			want: ClassTransient,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(errors.New(tc.msg)); got != tc.want {
				t.Fatalf("Classify(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

// TestClassifyDispositionBeatsText 第一级（Disposition 单源）优先于第二级文本。
//
// 「类型说了 A、文案说 B 时听文案」与判定纪律相反：上游判成 authentication 的
// 失败即使文案里带「connection」也不得被文本兜底翻成 transient（那会多烧一次
// 跨域改派）；上游判成 permanent 的失败即使文案带 403 也不得翻成 fatal。
func TestClassifyDispositionBeatsText(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Class
	}{
		{
			name: "上游 authentication 压过文本里的 connection",
			err:  toolfail.ClassifyAs(toolfail.ClassAuthentication, errors.New("connection refused")),
			want: ClassFatal,
		},
		{
			name: "上游 permission_denied 压过文本里的 timeout",
			err:  toolfail.ClassifyAs(toolfail.ClassPermissionDenied, errors.New("timeout waiting for grant")),
			want: ClassFatal,
		},
		{
			name: "上游 permanent 压过文本里的 403",
			err:  toolfail.ClassifyAs(toolfail.ClassPermanent, errors.New("unsupported op, status 403")),
			want: ClassOther,
		},
		{
			name: "上游 not_found 判不确定（换执行者可能指向别的目标）",
			err:  toolfail.ClassifyAs(toolfail.ClassNotFound, errors.New("target absent")),
			want: ClassOther,
		},
		{
			name: "上游 invalid_argument 判不确定（换执行者参数构造不同）",
			err:  toolfail.ClassifyAs(toolfail.ClassInvalidArgument, errors.New("bad field")),
			want: ClassOther,
		},
		{
			name: "上游 rate_limited 判瞬态",
			err:  toolfail.New(toolfail.ClassRateLimited, "429", "slow down"),
			want: ClassTransient,
		},
		{
			name: "上游 self_reported 判瞬态",
			err:  toolfail.ClassifyAs(toolfail.ClassSelfReported, errors.New("retry later")),
			want: ClassTransient,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Fatalf("Classify() = %q, want %q（err=%v）", got, tc.want, tc.err)
			}
		})
	}
}

// TestClassifiedTextIsLowercased 状态码与英文关键词按小写文本判定
// （宿主拼进来的文本常带大写状态名，大小写不归一会让同一失败分到不同档）。
func TestClassifiedTextIsLowercased(t *testing.T) {
	upper := "Upstream CONNECT refused: Service Unavailable"
	if got := Classify(errors.New(upper)); got != ClassTransient {
		t.Fatalf("大写文本应仍判瞬态，got %q", got)
	}
	// 词表本身是小写的，这里顺带钉住归一只发生在判定面、不改写 err 文本。
	if !strings.Contains(errors.New(upper).Error(), "CONNECT") {
		t.Fatal("判定不得改写错误原文")
	}
}

// TestClassifyChineseTexts 词表中英双语对称（issue #207）：中文瞬态必须判
// transient（跨域兜底可达）、中文权限必须判 fatal、裸「拒绝」收窄后不再把
// 无权限语义的拒绝文案误升级成终态。修复前：中文瞬态整类不可达——
// 「连接被拒绝」被裸「拒绝」判 fatal 直接终态、「连接超时」降级为仅同域。
func TestClassifyChineseTexts(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want Class
	}{
		{
			name: "中文瞬态：连接被拒绝（双档词竞争按瞬态，验收①）",
			msg:  "连接被拒绝",
			want: ClassTransient,
		},
		{
			name: "中文瞬态：连接超时（修复前降级为不跨域的 other）",
			msg:  "上游连接超时",
			want: ClassTransient,
		},
		{
			name: "中文瞬态：暂时不可用",
			msg:  "依赖服务暂时不可用，请稍后重试",
			want: ClassTransient,
		},
		{
			name: "中文瞬态：限流",
			msg:  "触发限流，稍后再试",
			want: ClassTransient,
		},
		{
			name: "中文终态：权限拒绝（验收②）",
			msg:  "权限拒绝：角色不足以执行该操作",
			want: ClassFatal,
		},
		{
			name: "中文终态：拒绝访问",
			msg:  "网关返回：拒绝访问",
			want: ClassFatal,
		},
		{
			name: "中文终态：权限词本就命中",
			msg:  "缺少审批：需主管签批",
			want: ClassFatal,
		},
		{
			name: "裸「拒绝」已收窄：无权限语义的拒绝不得误判终态",
			msg:  "请求被对方拒绝，参数不符合约定",
			want: ClassOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(errors.New(tc.msg)); got != tc.want {
				t.Fatalf("Classify(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}
