#!/usr/bin/env node
// gen.mjs 解析判据的反向验证。
//
// 为什么要有这个文件：gen.mjs 产出的每个数字都进到站点的「机器事实」页，
// 而**解析失效的失败形态是静默的**——某条规则没被解析到时，页面只是少一行，
// 读起来完全正常，没有任何东西会红。这正是本仓反复说的那类病：判据变绿。
//
// 本轮的实际触发：loadRules 的 id 正则写死了 `harness-` 前缀，于是
// `tool-pairing-key-single-source` 这条规则被静默漏掉，站点「门禁规则清单」
// 写着 9 条、实际 policy 里 10 条（README 写 10 条，三处对不上且没人发现）。
//
// 做法：从 gen.mjs **源码里抽出 loadRules 本体**再喂构造输入——
// 这样测的是真正在跑的那份实现，而不是复制出来的副本（副本与实现一漂移，
// 测试就退化成「看着在、其实没判」）。
//
// 判据三条：
//   ① 非 harness- 前缀的规则也要被解析到（写死前缀 ⇒ 静默少一条）；
//   ② 段内 `- id:` 条数必须与解析出 desc 的条数一致（缺 desc / 字段改名 ⇒ 红）；
//   ③ 没有 rules 段 ⇒ 红。
//
// 接线：npm run docs:build / docs:dev 的第一步（`node gen.test.mjs`）。
// 它跑在站点构建前面 ⇒ 解析判据一坏，构建就红，不会带着少几条规则的清单上线。

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const siteDir = dirname(fileURLToPath(import.meta.url))
const repo = join(siteDir, '..')

function fail(msg) {
  console.error('gen.test.mjs: ' + msg)
  process.exit(1)
}

// 抽出 gen.mjs 里真正在跑的 loadRules（不复制实现）。
const genSrc = readFileSync(join(siteDir, 'gen.mjs'), 'utf8')
const fnMatch = /function loadRules\(src\) \{[\s\S]*?\n\}/.exec(genSrc)
if (!fnMatch) fail('gen.mjs 里找不到 loadRules——函数改名或搬家了？本用例已失效，必须同步改名')
const loadRules = new Function('fail', 'return (' + fnMatch[0] + ')')(function fail(msg) {
  throw new Error(msg)
})

let failed = 0
function check(name, fn) {
  try {
    fn()
    console.log('  PASS  ' + name)
  } catch (e) {
    failed++
    console.error('  FAIL  ' + name + '  →  ' + e.message)
  }
}

console.log('gen.test.mjs —— loadRules 判据反向验证')

check('T1 非 harness- 前缀的规则被解析到（写死前缀会静默少一条）', () => {
  const yaml = [
    'version: 1',
    'rules:',
    '  - id: harness-a',
    '    desc: jia',
    '  - id: tool-pairing-key-single-source',
    '    desc: yi',
    'facades: []',
    'sandbox_providers:',
    '  providers:',
    '    - id: gatecheck-managed',
    '      level: LevelProcess',
  ].join('\n')
  const rules = loadRules(yaml)
  if (rules.length !== 2) throw new Error('应解析到 2 条，实得 ' + rules.length)
  if (rules[1].id !== 'tool-pairing-key-single-source') throw new Error('非 harness- 前缀的规则被漏掉')
})

check('T2 段内有 `- id:` 却解析不出 desc ⇒ 判红（缺 desc / 字段改名 / 缩进变了）', () => {
  const yaml = [
    'rules:',
    '  - id: harness-a',
    '    desc: jia',
    '  - id: harness-b',
    '    severity: error',
    'facades: []',
  ].join('\n')
  let threw = false
  try { loadRules(yaml) } catch (e) { threw = true }
  if (!threw) throw new Error('应当判红，实际放行了（清单会少一条规则而没人发现）')
})

check('T3 没有 rules 段 ⇒ 判红', () => {
  let threw = false
  try { loadRules(['version: 1', 'modules:', '  - id: x'].join('\n')) } catch (e) { threw = true }
  if (!threw) throw new Error('应当判红，实际放行了')
})

check('T4 真 policy：解析条数 == 段内 `- id:` 条数，且含非 harness- 前缀规则', () => {
  const policy = readFileSync(join(repo, 'architecture-policy.yaml'), 'utf8')
  const rules = loadRules(policy)
  const sec = policy.split('\nrules:', 2)[1].split(/\n(?=[A-Za-z_][\w]*:)/)[0]
  const ids = [...sec.matchAll(/^\s*- id: ([\w-]+)/gm)].map((m) => m[1])
  if (rules.length !== ids.length) {
    throw new Error('条数对不上：段内 ' + ids.length + ' 条，解析出 ' + rules.length + ' 条')
  }
  if (!ids.some((id) => !id.startsWith('harness-'))) {
    throw new Error('policy 里一条非 harness- 前缀的规则都没有——T1 的前提没了，本用例已失去意义')
  }
  for (const id of ids) {
    if (!rules.some((r) => r.id === id)) throw new Error('规则 ' + id + ' 没被解析到')
  }
  console.log('        （真 policy：' + rules.length + ' 条规则全部解析到）')
})

if (failed) {
  console.error('gen.test.mjs: ' + failed + ' 条判据未通过——拒绝构建站点')
  process.exit(1)
}
console.log('gen.test.mjs: 全部通过')
