// Copying text has to work on every deployment of this product, including the
// plain-HTTP internal addresses group customers actually use
// (http://192.168.8.6:8080 and similar).
//
// The async Clipboard API is only exposed in a secure context, so on those hosts
// `navigator.clipboard` is `undefined` and a bare `navigator.clipboard.writeText`
// call throws a TypeError before any permission question is even asked. That is
// why "查看业务系统详情，点击复制编码报错" and copying an API token both failed
// with a generic error while working perfectly on the developer's localhost.
//
// Falling back to a hidden textarea plus execCommand keeps the button working
// everywhere; a copied non-secret value such as a system code is exactly the
// case where a synchronous fallback is appropriate.

export async function copyTextToClipboard(text: string): Promise<boolean> {
  if (!text) return false
  const secureClipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined
  if (secureClipboard?.writeText) {
    try {
      await secureClipboard.writeText(text)
      return true
    } catch {
      // Permission denied or a non-focused document: fall through to the
      // legacy path instead of failing the user's action.
    }
  }
  return legacyCopy(text)
}

function legacyCopy(text: string): boolean {
  if (typeof document === 'undefined') return false
  const textarea = document.createElement('textarea')
  textarea.value = text
  // Keep it off-screen but still focusable, and avoid scrolling the page.
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '-9999px'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  const selection = document.getSelection()
  const previousRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null
  try {
    textarea.select()
    textarea.setSelectionRange(0, text.length)
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(textarea)
    if (previousRange && selection) {
      selection.removeAllRanges()
      selection.addRange(previousRange)
    }
  }
}
