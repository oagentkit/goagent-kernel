// Package sqliteadapter 单机持久化的**真 SQLite 参考实现**（占位包声明）。
//
// # 为什么本文件现在只有包声明
//
// 依赖方向门禁要求「新增包先在 architecture-policy.yaml 登记」，而 policy 自校验
// 要求「登记的包根必须真实存在」——两条合起来意味着登记之后必须立刻有落地的
// 包目录，否则门禁当场红（陈旧包根当天即红）。适配器实现由后续任务补齐，此处
// 先给出一个最小可编译的包声明：不含任何导出符号，不改变任何行为。
//
// # 落库形态（后续任务展开）
//
// 单文件 SQLite(WAL)：journal_mode=WAL / synchronous=FULL / busy_timeout=5000，
// 写事务一律 BEGIN IMMEDIATE（先拿写锁再干活）；事件 ID 由每租户一行水位表
// 铸造（UPDATE … RETURNING，回滚不烧号 ⇒ 无空洞）。
//
// 生命周期: Experimental（v0.50.0 定级）——参考实现，随契约套件与参考部署演进，
// 不承诺任何兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
package sqliteadapter
