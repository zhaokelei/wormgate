import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App as AntApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'

import 'antd/dist/reset.css'
import './styles.css'

import App from './App'
import { AuthProvider } from './auth'
import { I18nProvider, useI18n } from './i18n'
import { ThemeProvider, useThemeMode } from './theme'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false }
  }
})

/** Reads the theme and language context, so ConfigProvider must sit under both. */
function Root() {
  const { themeConfig } = useThemeMode()
  const { lang } = useI18n()
  return (
    <ConfigProvider
      locale={lang === 'en' ? enUS : zhCN}
      theme={themeConfig}
      // antd pads two-character Chinese labels ("登 录"). The Vue consoles do
      // not, and next to them the gap reads as a typo rather than spacing.
      button={{ autoInsertSpace: false }}
    >
      <AntApp>
        <QueryClientProvider client={queryClient}>
          <BrowserRouter basename="/next">
            <AuthProvider>
              <App />
            </AuthProvider>
          </BrowserRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <I18nProvider>
        <Root />
      </I18nProvider>
    </ThemeProvider>
  </StrictMode>
)
