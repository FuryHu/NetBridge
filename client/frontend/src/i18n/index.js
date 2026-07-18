// NetBridge 轻量国际化。
//
// 刻意不引入 vue-i18n 之类的库：3 种语言 + ~60 条文案总量很小，
// 一个基于 Vue ref 的 t() 即可覆盖，避免给二进制（wails embed frontend/dist）
// 增加不必要的体积。切换语言时，t() 读取的 locale.value 变化会触发模板重渲染。
import {ref} from 'vue'
import zh from './locales/zh'
import en from './locales/en'
import ja from './locales/ja'

export const messages = {zh, en, ja}

// 语言列表（label 用各自语言的写法，不随当前语言变化，符合直觉）。
export const LANGS = [
  {code: 'zh', label: '中文'},
  {code: 'en', label: 'English'},
  {code: 'ja', label: '日本語'},
]

const STORAGE_KEY = 'netbridge_locale'

// 探测初始语言：localStorage > 浏览器/系统语言 > 默认英文。
function detectLocale() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved && messages[saved]) return saved
  } catch {}
  const nav = (typeof navigator !== 'undefined' && (navigator.language || navigator.userLanguage)) || ''
  const lower = nav.toLowerCase()
  if (lower.startsWith('zh')) return 'zh'
  if (lower.startsWith('ja')) return 'ja'
  return 'en' // 其余一律英文兜底
}

export const locale = ref(detectLocale())

// setLocale 切换语言并持久化；同步 <html lang> 供无障碍/输入法参考。
export function setLocale(code) {
  if (!messages[code]) return
  locale.value = code
  try {localStorage.setItem(STORAGE_KEY, code)} catch {}
  try {document.documentElement.lang = code === 'zh' ? 'zh-CN' : code} catch {}
}

// t(key, params) 取当前语言文案；缺失时回退英文，再缺失回退 key 本身。
// params 支持 {name} 占位符插值。在模板里调用会随 locale 变化自动重渲染。
export function t(key, params) {
  const dict = messages[locale.value] || messages.en
  let s = dict[key]
  if (s === undefined) s = (messages.en[key] !== undefined ? messages.en[key] : key)
  if (params) {
    for (const k in params) {
      s = s.replace(new RegExp('\\{' + k + '\\}', 'g'), params[k])
    }
  }
  return s
}
