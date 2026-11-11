/**
 * Bilingual layer for the React console (Chinese primary, English secondary).
 *
 * Why the same DOM-layer trick as the Vue consoles: the catalogue in
 * `web/common/locales.js` already covers API error bodies, toast text and
 * streamed log lines. Reusing it — instead of threading `t()` through every
 * component — keeps one translation source for all three consoles and makes a
 * switch in `/admin/` and `/next/` behave identically.
 *
 * The language lives in `wormgate.lang`, the key the Vue consoles write, so a
 * choice made in one console is picked up by the other on the next load.
 *
 * React writes text nodes on re-render, so every node keeps its authored source
 * in a WeakMap. A translated string is never translated twice, and a fresh
 * render (new numbers, new rows) is read as a new source.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState
} from 'react'
import type { ReactNode } from 'react'

// @ts-ignore - plain-JS catalogue shared by the Vue consoles
import { EN_ZH, ZH_EN, ZH_FALLBACK, ZH_PATTERNS } from '@common/locales'

export type Lang = 'zh' | 'en'

const STORAGE_KEY = 'wormgate.lang'
const ATTRS = ['placeholder', 'title', 'aria-label', 'alt'] as const
const SKIP_TAGS = new Set(['SCRIPT', 'STYLE', 'TEXTAREA'])
const HAS_ZH = /[\u4e00-\u9fff]/
const CJK = /[\u4e00-\u9fff]+/g

type Rule = [string, string]

const zhRules: Rule[] = Object.entries(ZH_EN as Record<string, string>).sort(
  (a, b) => b[0].length - a[0].length
)
const enRules: Rule[] = Object.entries(EN_ZH as Record<string, string>).sort(
  (a, b) => b[0].length - a[0].length
)
const fallbackRules: Rule[] = Object.entries(ZH_FALLBACK as Record<string, string>).sort(
  (a, b) => b[0].length - a[0].length
)
const patterns = ZH_PATTERNS as Array<[RegExp, (m: RegExpMatchArray) => string]>

function readLang(): Lang {
  try {
    if (localStorage.getItem(STORAGE_KEY) === 'en') return 'en'
    // Compatibility: read the pre-rename key once, then persist under the new one.
    if (localStorage.getItem('tunnelx.lang') === 'en') {
      localStorage.setItem(STORAGE_KEY, 'en')
      return 'en'
    }
    return 'zh'
  } catch {
    return 'zh'
  }
}

/**
 * Interpolated strings can leave isolated Chinese fragments behind. Map the
 * common ones, then drop whatever is still unresolved: a shorter English label
 * beats a half-translated one.
 */
function finalize(out: string) {
  if (!HAS_ZH.test(out)) return out
  for (const [zh, en] of fallbackRules) {
    if (out.includes(zh)) out = out.split(zh).join(en)
  }
  return out
    .replace(CJK, '')
    .replace(/[ \t]{2,}/g, ' ')
    .replace(/\s+([,.:)])/g, '$1')
    .trim()
}

export function translate(text: string, to: Lang): string {
  if (!text || typeof text !== 'string' || !text.trim()) return text
  if (to === 'en') {
    if (!HAS_ZH.test(text)) return text
    // Exact phrases first: a broad pattern such as /^入站 (.+)$/ would otherwise
    // swallow compound labels and skip the glossary entirely.
    const trimmed = text.trim()
    if (ZH_EN[trimmed]) return finalize(text.replace(trimmed, ZH_EN[trimmed]))
    for (const [re, render] of patterns) {
      const m = text.match(re)
      if (m) return finalize(render(m))
    }
    let out = text
    for (const [zh, en] of zhRules) {
      if (!HAS_ZH.test(out)) break
      if (out.includes(zh)) out = out.split(zh).join(en)
    }
    return finalize(out)
  }
  // Chinese mode: turn backend English into Chinese; authored Chinese stays.
  if (HAS_ZH.test(text)) return text
  const trimmed = text.trim()
  if (EN_ZH[trimmed]) return text.replace(trimmed, EN_ZH[trimmed])
  let out = text
  for (const [en, zh] of enRules) {
    if (out.includes(en)) out = out.split(en).join(zh)
  }
  return out
}

// -- DOM translation ------------------------------------------------------

const textSrc = new WeakMap<Text, string>()
const textOut = new WeakMap<Text, string>()
const attrSrc = new WeakMap<Element, Record<string, string>>()
const attrOut = new WeakMap<Element, Record<string, string>>()

/**
 * A node is re-read from the DOM whenever its value no longer matches what the
 * translator last wrote. That covers two different cases with one rule:
 *  - React re-rendered the node (new numbers, a toggle flipping its own label
 *    from "Switch to English" to "Switch to Chinese"), so the DOM is a new
 *    source, even during a forced re-translation;
 *  - nothing was re-rendered and only the language changed, so the authored
 *    source is reused and translated again.
 */
function applyText(node: Text, lang: Lang, force: boolean) {
  const current = node.nodeValue ?? ''
  const previous = textOut.get(node)
  const rewritten = previous !== current
  if (!force && !rewritten) return
  const cached = textSrc.get(node)
  const source = rewritten || cached === undefined ? current : cached
  textSrc.set(node, source)
  const next = translate(source, lang)
  textOut.set(node, next)
  if (next !== current) node.nodeValue = next
}

function applyAttr(el: Element, name: string, lang: Lang, force: boolean) {
  const current = el.getAttribute(name)
  if (current === null) return
  let store = attrSrc.get(el)
  let outputs = attrOut.get(el)
  if (!store || !outputs) {
    store = {}
    outputs = {}
    attrSrc.set(el, store)
    attrOut.set(el, outputs)
  }
  const previous = outputs[name]
  const rewritten = previous !== current
  if (!force && !rewritten) return
  const cached = store[name]
  const source = rewritten || cached === undefined ? current : cached
  store[name] = source
  const next = translate(source, lang)
  outputs[name] = next
  if (next !== current) el.setAttribute(name, next)
}

function visit(node: Node, lang: Lang, force: boolean) {
  if (node.nodeType === Node.TEXT_NODE) {
    const parent = (node as Text).parentElement
    // A skip marker guards its own text too, not just its attributes and
    // children: the "中" on the language button would otherwise be read as a
    // stray Chinese fragment and blanked out in English.
    if (parent && !SKIP_TAGS.has(parent.tagName) && !parent.closest('[data-i18n-skip]')) {
      applyText(node as Text, lang, force)
    }
    return
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return
  const el = node as Element
  if (el.hasAttribute('data-i18n-skip')) return
  for (const name of ATTRS) {
    if (el.hasAttribute(name)) applyAttr(el, name, lang, force)
  }
}

function walk(root: Node, lang: Lang, force: boolean) {
  visit(root, lang, force)
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT)
  let node: Node | null
  while ((node = walker.nextNode())) visit(node, lang, force)
}

/**
 * One observer for the app lifetime. It picks up antd's portal layers (dropdown,
 * modal, message) because they mount under <body>, and it picks up React's own
 * text writes, which is what keeps a page that re-renders after a switch fully
 * translated.
 */
function translateDocument(lang: Lang, force: boolean) {
  if (!document.body) return
  walk(document.body, lang, force)
}

interface I18nCtxValue {
  lang: Lang
  setLang: (next: Lang) => void
  toggle: () => void
}

const I18nCtx = createContext<I18nCtxValue>({ lang: 'zh', setLang: () => {}, toggle: () => {} })

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(readLang)
  const langRef = useRef(lang)
  const observer = useRef<MutationObserver | null>(null)

  // Translate whatever is on screen first, then watch for anything React adds
  // afterwards (route changes, table rows, toasts).
  useEffect(() => {
    langRef.current = lang
    document.documentElement.setAttribute('lang', lang === 'zh' ? 'zh-CN' : 'en')
    translateDocument(lang, true)

    if (observer.current) return
    observer.current = new MutationObserver((records) => {
      for (const record of records) {
        if (record.type === 'childList') {
          for (const added of record.addedNodes) walk(added, langRef.current, false)
        } else if (record.type === 'characterData') {
          visit(record.target, langRef.current, false)
        } else if (record.type === 'attributes' && record.attributeName) {
          visit(record.target, langRef.current, false)
        }
      }
    })
    observer.current.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: [...ATTRS]
    })
  }, [lang])

  useEffect(() => () => observer.current?.disconnect(), [])

  const setLang = useCallback((next: Lang) => {
    const value: Lang = next === 'en' ? 'en' : 'zh'
    try {
      localStorage.setItem(STORAGE_KEY, value)
    } catch {
      /* private mode: the in-memory choice still holds for this session */
    }
    setLangState(value)
  }, [])

  const value = useMemo<I18nCtxValue>(
    () => ({ lang, setLang, toggle: () => setLang(lang === 'zh' ? 'en' : 'zh') }),
    [lang, setLang]
  )

  return <I18nCtx.Provider value={value}>{children}</I18nCtx.Provider>
}

export function useI18n() {
  return useContext(I18nCtx)
}
