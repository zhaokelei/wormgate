/**
 * Lightweight bilingual UI layer (Chinese primary, English secondary).
 *
 * Design notes:
 *  - The source of truth for UI copy stays Chinese in the templates. Instead of
 *    rewriting hundreds of hardcoded strings into t('key'), translations are
 *    applied at the DOM layer, which also covers dynamic content that no
 *    string-extraction pass can see: API error bodies, toast messages and SSE
 *    log lines.
 *  - Language is stored in localStorage. It defaults to Chinese and NEVER
 *    follows navigator.language, so a manual switch to English persists.
 *  - Each text node keeps its original source in a WeakMap, so switching back
 *    and forth never translates an already translated string, and Vue updates
 *    (new numbers, new log lines) are picked up as fresh sources.
 */
import { ref } from 'vue'
import { ZH_EN, ZH_PATTERNS, ZH_FALLBACK, EN_ZH } from './locales'

const STORAGE_KEY = 'wormgate.lang'
const ATTRS = ['placeholder', 'title', 'aria-label', 'alt']
const SKIP_TAGS = new Set(['SCRIPT', 'STYLE', 'TEXTAREA'])
const HAS_ZH = /[\u4e00-\u9fff]/

function readLang() {
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

export const lang = ref(readLang())
const zhRules = Object.entries(ZH_EN).sort((a, b) => b[0].length - a[0].length)
const enRules = Object.entries(EN_ZH).sort((a, b) => b[0].length - a[0].length)
const fallbackRules = Object.entries(ZH_FALLBACK).sort((a, b) => b[0].length - a[0].length)
const CJK = /[\u4e00-\u9fff]+/g

/** Translate a single string into the requested language. */
function finalize(out) {
  if (!HAS_ZH.test(out)) return out
  // Interpolated text can leave isolated Chinese fragments behind. Map the
  // common ones, then strip anything still unresolved: a slightly shorter
  // English label is better than a half-translated one.
  for (const [zh, en] of fallbackRules) {
    if (out.includes(zh)) out = out.split(zh).join(en)
  }
  return out.replace(CJK, '').replace(/[ \t]{2,}/g, ' ').replace(/\s+([,.:)])/g, '$1').trim()
}

export function t(text, to = lang.value) {
  if (!text || typeof text !== 'string' || !text.trim()) return text
  if (to === 'en') {
    if (!HAS_ZH.test(text)) return text
    // Exact phrases first: broad patterns such as /^入站 (.+)$/ would
    // otherwise swallow compound labels and skip the glossary entirely.
    const trimmed = text.trim()
    if (ZH_EN[trimmed]) return finalize(text.replace(trimmed, ZH_EN[trimmed]))
    for (const [re, render] of ZH_PATTERNS) {
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

export function setLang(next) {
  const value = next === 'en' ? 'en' : 'zh'
  try {
    localStorage.setItem(STORAGE_KEY, value)
  } catch {
    /* private mode: keep the in-memory choice */
  }
  lang.value = value
  document.documentElement.setAttribute('lang', value === 'zh' ? 'zh-CN' : 'en')
  applyAll(true)
}

export function toggleLang() {
  setLang(lang.value === 'zh' ? 'en' : 'zh')
}

// -- DOM translation ------------------------------------------------------

const textSrc = new WeakMap()
const textOut = new WeakMap()
const attrSrc = new WeakMap()
const attrOut = new WeakMap()
let observer = null

function applyText(node, force) {
  const current = node.nodeValue
  if (!force && textOut.get(node) === current) return
  let source = textSrc.get(node)
  if (force !== true && (source === undefined || textOut.get(node) !== current)) {
    source = current
  }
  if (source === undefined) source = current
  textSrc.set(node, source)
  const next = t(source)
  textOut.set(node, next)
  if (next !== current) node.nodeValue = next
}

function applyAttr(el, name, force) {
  const current = el.getAttribute(name)
  if (current === null) return
  let store = attrSrc.get(el)
  let outputs = attrOut.get(el)
  if (!store) {
    store = {}
    outputs = {}
    attrSrc.set(el, store)
    attrOut.set(el, outputs)
  }
  if (!force && outputs[name] === current) return
  let source = store[name]
  if (force !== true && (source === undefined || outputs[name] !== current)) {
    source = current
  }
  if (source === undefined) source = current
  store[name] = source
  const next = t(source)
  outputs[name] = next
  if (next !== current) el.setAttribute(name, next)
}

function visit(node, force = false) {
  if (node.nodeType === Node.TEXT_NODE) {
    // A skip marker also guards its own text, not just attributes: the "中" on
    // the language button would otherwise read as a stray Chinese fragment and
    // be stripped in English mode.
    const parent = node.parentElement
    if (parent && !SKIP_TAGS.has(parent.tagName) && !parent.closest('[data-i18n-skip]')) {
      applyText(node, force)
    }
    return
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return
  if (node.hasAttribute('data-i18n-skip')) return
  for (const name of ATTRS) {
    if (node.hasAttribute(name)) applyAttr(node, name, force)
  }
}

function applyAll(force = false) {
  if (!document.body) return
  const walker = document.createTreeWalker(
    document.body,
    NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT,
    null
  )
  const nodes = []
  let node
  while ((node = walker.nextNode())) nodes.push(node)
  for (const n of nodes) visit(n, force)
}

function applySubtree(root, force = false) {
  if (!root) return
  if (root.nodeType === Node.TEXT_NODE) {
    visit(root, force)
    return
  }
  if (root.nodeType !== Node.ELEMENT_NODE) return
  const walker = document.createTreeWalker(
    root,
    NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT,
    null
  )
  const nodes = [root]
  let node
  while ((node = walker.nextNode())) nodes.push(node)
  for (const n of nodes) visit(n, force)
}

function install() {
  document.documentElement.setAttribute('lang', lang.value === 'zh' ? 'zh-CN' : 'en')
  // Delegated switch, so even plain pages such as the login screen can offer a
  // language toggle without importing anything.
  document.addEventListener('click', (event) => {
    if (event.target?.closest?.('[data-i18n-toggle]')) toggleLang()
  }, true)
  applyAll(true)
  if (observer) return
  observer = new MutationObserver((records) => {
    for (const record of records) {
      if (record.type === 'childList') {
        for (const added of record.addedNodes) applySubtree(added)
      } else if (record.type === 'characterData') {
        visit(record.target)
      } else if (record.type === 'attributes' && record.attributeName) {
        visit(record.target)
      }
    }
  })
  observer.observe(document.body, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: ATTRS
  })
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', install, { once: true })
  } else {
    install()
  }
}
