import { onScopeDispose, watch } from 'vue'

export function useLiveList(filters: () => unknown, search: () => unknown, text?: () => string, context?: () => unknown) {
  const signature = () => JSON.stringify(filters())
  let sequence = 0
  let observedRequest = 0
  let lastStarted: string | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false

  function cancelPending() {
    clearTimeout(timer)
    timer = undefined
  }

  watch([signature, () => text?.() ?? ''], ([current, currentText], [, previousText]) => {
    cancelPending()
    const alreadyStarted = current === lastStarted && sequence > observedRequest
    observedRequest = sequence
    if (alreadyStarted) return
    if (currentText && currentText !== previousText) timer = setTimeout(() => { timer = undefined; search() }, 250)
    else search()
  })

  onScopeDispose(() => {
    disposed = true
    cancelPending()
  })

  function begin() {
    cancelPending()
    const request = ++sequence
    const started = signature()
    const startedContext = JSON.stringify(context?.())
    lastStarted = started
    return () => !disposed && request === sequence && started === signature() && startedContext === JSON.stringify(context?.())
  }

  return { begin }
}
