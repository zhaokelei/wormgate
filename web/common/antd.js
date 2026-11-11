/**
 * Ant Design Vue wiring shared by both consoles.
 *
 * The reference panel (3x-ui v3.x) is React + Ant Design 6. Vue has no antd 6,
 * so this project uses ant-design-vue 4 — the same design language and token
 * system, rendered by antd's own Vue port. Rather than re-implementing every
 * widget by hand, the consoles now compose real antd components (Layout, Menu,
 * Card, Statistic, Table, Form, Tabs, Modal...) and only keep custom CSS for
 * things antd has no opinion about.
 *
 * Token values below are the antd v6 dark canvas: near-black layout, slightly
 * lifted containers, hairline borders, 8px radius. Changing mode swaps the
 * algorithm, exactly like <ConfigProvider theme={{ algorithm }}> upstream.
 */
import 'ant-design-vue/dist/reset.css'

import { theme as antdTheme } from 'ant-design-vue'
import zhCN from 'ant-design-vue/es/locale/zh_CN'

export const antdLocale = zhCN

const FONT =
  "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB', " +
  "'Microsoft YaHei', 'Noto Sans SC', Roboto, Helvetica, Arial, sans-serif"
const MONO =
  "ui-monospace, SFMono-Regular, 'JetBrains Mono', 'Cascadia Mono', Consolas, " +
  "'Liberation Mono', monospace"

const DARK = {
  colorPrimary: '#1677ff',
  colorInfo: '#1677ff',
  colorSuccess: '#52c41a',
  colorWarning: '#faad14',
  colorError: '#ff4d4f',
  colorBgLayout: '#08090c',
  colorBgContainer: '#101318',
  colorBgElevated: '#161a21',
  colorBorder: '#232833',
  colorBorderSecondary: '#1b202a',
  colorText: '#e6e8ec',
  colorTextSecondary: '#9aa4b2',
  colorTextTertiary: '#6b7280',
  borderRadius: 8,
  fontFamily: FONT,
  fontFamilyCode: MONO
}

const LIGHT = {
  colorPrimary: '#1677ff',
  colorBgLayout: '#f5f5f5',
  colorBgContainer: '#ffffff',
  colorBorder: '#e5e7eb',
  colorBorderSecondary: '#f0f0f0',
  borderRadius: 8,
  fontFamily: FONT,
  fontFamilyCode: MONO
}

/**
 * Build a ConfigProvider theme for the given mode.
 * @param {'dark'|'light'} mode
 */
export function antdThemeConfig(mode) {
  const dark = mode !== 'light'
  return {
    algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token: dark ? DARK : LIGHT,
    components: {
      Layout: {
        bodyBg: 'transparent',
        headerBg: 'transparent',
        siderBg: dark ? '#0d1015' : '#ffffff',
        triggerBg: 'transparent'
      },
      Menu: {
        itemBg: 'transparent',
        subMenuItemBg: 'transparent',
        darkItemBg: 'transparent',
        darkSubMenuItemBg: 'transparent',
        itemHeight: 38,
        itemMarginInline: 8,
        itemBorderRadius: 8
      },
      Card: { headerFontSize: 13, headerHeight: 46, paddingLG: 16 },
      Table: { headerBg: dark ? '#141821' : '#fafafa', headerSplitColor: 'transparent', rowHoverBg: dark ? '#161b24' : '#f5f7fa' },
      Statistic: { contentFontSize: 26 }
    }
  }
}
