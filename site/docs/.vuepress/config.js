import { viteBundler } from '@vuepress/bundler-vite'
import { defaultTheme } from '@vuepress/theme-default'
import { defineUserConfig } from 'vuepress'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// ADR 清单由 site/gen.mjs 生成（页面本体也在生成物里）。
//
// 为什么清单不写死在本文件里：每加一篇 ADR 就要手改一次侧栏配置，
// 而那一次手改总会有人忘——侧栏与实际页数就此漂移，且没有任何东西会红。
// 故清单从生成物读：页数变了，配置自动跟着变。
//
// 清单文件不存在时直接报错而不是静默用空数组：那是构建顺序错了
// （npm run docs:build 会先跑 node gen.mjs），不是「本仓没有 ADR」。
let adrManifest = []
try {
  adrManifest = JSON.parse(readFileSync(new URL('./adr-manifest.json', import.meta.url), 'utf8'))
} catch (e) {
  console.error('config.js: 读不到 adr-manifest.json —— 先跑 node gen.mjs（npm run docs:build 会自动先跑）')
  throw e
}

const here = path.dirname(fileURLToPath(import.meta.url))

// 站点配置。base 必须是 /goagent-kernel/——GitHub Pages 项目站挂在子路径下，
// 写错根路径整站资源 404。
export default defineUserConfig({
  lang: 'zh-CN',
  title: 'goagent-kernel',
  description: '编排是代码，LLM 只做单环节——多智能体编排的通用内核：机制面，不认识任何领域概念。',
  base: '/goagent-kernel/',
  head: [['link', { rel: 'icon', href: '/goagent-kernel/favicon.svg' }]],
  bundler: viteBundler(),
  // 用别名覆盖把「白 / 暗 / 自动」三态开关换进顶栏。
  //
  // 为什么必须覆盖主题自带的那个按钮：VuePress 2 的原生开关只有两态
  // （亮 ⇄ 暗），「自动」只是**首次访问**时的取值来源（prefers-color-scheme），
  // 用户手动切过之后就再也回不到「跟随系统」。而主题内部的存储模型本来
  // 就是三态（localStorage['vuepress-color-scheme'] ∈ auto|light|dark），
  // 缺的只是一个能显式选中「自动」的界面入口 ⇒ 这里补的是**入口**，
  // 不是另造一套配色逻辑。
  //
  // 为什么用别名覆盖而不是改主题源码：别名是 VuePress 的公开机制
  // （app.pluginApi.hooks.alias）。用户插件在主题插件**之后**注册
  // ⇒ 同名键后者覆盖前者；vite 侧按键长降序匹配 ⇒ 具体的
  // '@theme/VPToggleColorModeButton.vue' 一定先于 '@theme' 前缀命中。
  plugins: [
    {
      name: 'kernel-theme-mode-switch',
      alias: {
        '@theme/VPToggleColorModeButton.vue': path.resolve(here, './components/ThemeModeSwitch.vue'),
      },
    },
  ],
  theme: defaultTheme({
    repo: 'yi-nology/goagent-kernel',
    repoLabel: 'GitHub',
    editLink: false,
    lastUpdated: false,
    contributors: false,
    // 白 / 暗 / 自动三态（2026-11-23 改）：此前是纯深色 + 关闭切换。
    // colorMode: 'auto' ⇒ 首次访问按 prefers-color-scheme 定；用户手动切过后
    // 走 localStorage。colorModeSwitch: true ⇒ 顶栏出切换按钮——但原生那个
    // 只有两态，三态入口由上面 plugins 里的别名覆盖提供（见 ThemeModeSwitch.vue）。
    // 亮色模式要有自己的配色板，否则亮色下沿用深色变量 ⇒ 浅色字压深色底，读不了。
    colorMode: 'auto',
    colorModeSwitch: true,
    // 关掉 changelog 挂件：它会在页面上浮出「commit/<hash>」胶囊（半截裁在
    // 边缘），对读者是噪声——版本史看仓内 CHANGELOG，入口在参考区。
    changelog: false,
    navbar: [
      { text: '介绍', link: '/guide/introduction.html' },
      { text: '设计初衷', link: '/guide/philosophy.html' },
      { text: '怎么用', link: '/guide/getting-started.html' },
      { text: '能力', link: '/guide/capabilities.html' },
      { text: '模型接入', link: '/guide/llm-guide.html' },
      { text: '坑速查', link: '/reference/pitfalls.html' },
      {
        text: 'AI 直读',
        children: [
          { text: '本站 llms.txt', link: 'https://yi-nology.github.io/goagent-kernel/llms.txt' },
          { text: 'goagent-runtime llms.txt', link: 'https://yi-nology.github.io/goagent-runtime/llms.txt' },
          { text: 'oekit llms.txt', link: 'https://oekit.github.io/llms.txt' },
          { text: 'oagentkit llms.txt', link: 'https://oagentkit.github.io/llms.txt' },
        ],
      },
      {
        text: '参考',
        children: [
          { text: '包清单与机器事实', link: '/reference/packages.html' },
          { text: '门禁体系', link: '/reference/gates.html' },
          { text: '决策记录（ADR）', link: '/reference/adr.html' },
          { text: '版本记录', link: '/reference/releases.html' },
        ],
      },
    ],
    footer: 'MIT License · github.com/yi-nology/goagent-kernel · <a href="https://yi-nology.github.io/goagent-kernel/llms.txt">AI 直读 llms.txt</a>',
    sidebar: {
      '/guide/': [
        {
          text: '指南',
          children: [
            '/guide/introduction.md',
            '/guide/philosophy.md',
            '/guide/getting-started.md',
            '/guide/capabilities.md',
            '/guide/llm-guide.md',
          ],
        },
      ],
      '/reference/': [
        {
          text: '参考',
          children: [
            '/reference/packages.md',
            '/reference/gates.md',
            '/reference/adr.md',
            '/reference/releases.md',
            '/reference/pitfalls.md',
          ],
        },
      ],
      '/adr/': [
        {
          text: '决策记录',
          children: adrManifest.map((a) => '/adr/' + a.file),
        },
      ],
    },
  }),
})
