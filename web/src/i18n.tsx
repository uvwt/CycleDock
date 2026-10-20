import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { I18nContext, type I18nValue, type Locale } from './i18n-context'
import { en, zhCN } from './i18n-messages'

const storageKey = 'cycledock.locale'

function initialLocale(): Locale {
  const stored = window.localStorage.getItem(storageKey)
  if (stored === 'en' || stored === 'zh-CN') return stored
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale)

  useEffect(() => {
    document.documentElement.lang = locale
    window.localStorage.setItem(storageKey, locale)
  }, [locale])

  const value = useMemo<I18nValue>(() => {
    const messages = locale === 'zh-CN' ? zhCN : en
    return {
      locale,
      setLocale: setLocaleState,
      t: (key) => messages[key],
    }
  }, [locale])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}
