package netguard

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// fakeResolver 确定性解析器：按名字查表，未登记的名字一律「解析失败」。
//
// 为什么测试必须自带解析器（issue #412）：判定路径要判的是「解析出来的地址
// 是不是私网」，可解析这一步依赖这台机器此刻能不能解析该域名。不注入时，
// 断言实际落在「此刻的网络状况」上：负载一抖就假红，且红点（解析失败）与
// 真因（网络抖动）指向两个排查方向。
//
// 键按 DNS 语义归一：大小写不敏感、尾点等价。
type fakeResolver map[string][]net.IP

// lookup 解析器接缝实现。
func (f fakeResolver) lookup(_ context.Context, host string) ([]net.IP, error) {
	key := strings.ToLower(strings.TrimSuffix(host, "."))
	ips, ok := f[key]
	if !ok {
		return nil, errors.New("解析失败（测试解析器未登记该名字）")
	}
	return ips, nil
}

// testResolver 测试用解析器：公网名 → 公网地址；回环族名 → 回环地址。
//
// 公网地址取 8.8.8.8：egress 的显式阻断表（0.0.0.0/8、CGNAT、三个
// TEST-NET、benchmark 段、240/4）都不含它——选一个「一定会被放行」的地址，
// 断言的红点才落在别处（名字层 / 私网段）而不是「这个 IP 恰好被拦」。
var testResolver = fakeResolver{
	"example.com":       {net.ParseIP("8.8.8.8")},
	"oapi.dingtalk.com": {net.ParseIP("8.8.8.8")},
	"localhost":         {net.ParseIP("127.0.0.1")},
	"api.localhost":     {net.ParseIP("127.0.0.1")},
}

// checkChain 逐跳校验（注入确定性解析器）。
//
// 逐跳校验里每一跳都要解析一次域名，所以「链条判得对不对」此前同样被绑在
// 「这台机器此刻能不能解析这些域名」上：起始跳公网名 + 相对 Location 的
// 干净链，用例能不能过取决于此刻的网络。
func checkChain(start string, hops []string, allow Allow, maxHops int) error {
	return CheckChainWithLookup(context.Background(), start, hops, allow, maxHops, testResolver.lookup)
}

// TestCheckURLUsesInjectedResolver 反向验证：注入的解析结果必须真的被用。
//
// 注入一个把公网名解析成**私网地址**的解析器，判定必须拒——若判定仍然走
// 真实 DNS（或者干脆不看注入结果），这条会绿着过，而「注入没生效」这件
// 事根本没人断言。
func TestCheckURLUsesInjectedResolver(t *testing.T) {
	priv := fakeResolver{"public.example": {net.ParseIP("10.0.0.7")}}
	if err := CheckURLWithLookup(context.Background(), "https://public.example/x", Allow{Private: false}, priv.lookup); err == nil {
		t.Fatal("注入的解析结果落在私网时必须拒（否则判定没用注入结果）")
	}
	// 同一个名字、同一个接缝，换成公网地址 ⇒ 放行。两条合起来才证明
	// 「结论随注入结果变」，而不是「这个 URL 恰好总被拒/总被放行」。
	pub := fakeResolver{"public.example": {net.ParseIP("8.8.8.8")}}
	if err := CheckURLWithLookup(context.Background(), "https://public.example/x", Allow{Private: false}, pub.lookup); err != nil {
		t.Fatalf("注入的公网地址应放行: %v", err)
	}
}

// TestCheckURLInjectedResolverFailure 解析失败必须报「出站目标解析失败」。
//
// 断言落在错误文本上：解析失败这一支此前只能靠外网抖动触发，且红点是
// 「no such host」——与「目标已拒绝」混在一起时，分不出是判定拒的还是
// 解析没成。
func TestCheckURLInjectedResolverFailure(t *testing.T) {
	empty := fakeResolver{}
	err := CheckURLWithLookup(context.Background(), "https://nowhere.example/x", Allow{}, empty.lookup)
	if err == nil {
		t.Fatal("解析失败必须报错")
	}
	if !strings.Contains(err.Error(), "出站目标解析失败") {
		t.Errorf("红点应是「出站目标解析失败」（实得 %v）", err)
	}
}

// TestCheckURLLiteralSkipsResolver IP 字面量不走解析器。
func TestCheckURLLiteralSkipsResolver(t *testing.T) {
	called := false
	boom := func(context.Context, string) ([]net.IP, error) {
		called = true
		return nil, errors.New("解析器不该被调用")
	}
	if err := CheckURLWithLookup(context.Background(), "http://10.0.0.5/x", Allow{Private: true}, boom); err != nil {
		t.Fatalf("私网地址在放行面下应通过: %v", err)
	}
	if called {
		t.Fatal("IP 字面量不得走解析器")
	}
}

// TestCheckURLNilLookupKeepsDefaultPath nil 接缝 ⇒ 缺省解析器（既有行为不变）。
//
// 这里刻意**不**拿真实域名去验「nil 真的走了系统解析器」——那正是本单要
// 从用例里拆掉的外网依赖。用 IP 字面量走一遍 nil 分支：它证明的是「nil
// 接缝不改变既有调用点的行为」，而「nil 时用 net.DefaultResolver」由
// defaultLookup 这一处实现承担（唯一真相）。
func TestCheckURLNilLookupKeepsDefaultPath(t *testing.T) {
	if err := CheckURLWithLookup(context.Background(), "http://10.0.0.5/x", Allow{Private: true}, nil); err != nil {
		t.Fatalf("nil 接缝下私网地址在放行面内应通过: %v", err)
	}
	if err := CheckURLWithLookup(context.Background(), "http://169.254.169.254/x", Allow{Private: true}, nil); err == nil {
		t.Fatal("nil 接缝下链路本地仍须拒（放行旗标不豁免它）")
	}
}

// TestCheckChainUsesInjectedResolver 逐跳校验同样走注入的解析器。
func TestCheckChainUsesInjectedResolver(t *testing.T) {
	priv := fakeResolver{"example.com": {net.ParseIP("10.0.0.7")}}
	err := CheckChainWithLookup(context.Background(), "https://example.com/start", []string{"/a"}, Allow{Private: false}, 0, priv.lookup)
	if err == nil {
		t.Fatal("注入的解析结果落在私网时链条必须拒（否则逐跳校验没用注入结果）")
	}
}
