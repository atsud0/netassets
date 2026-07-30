import axios from 'axios'

export interface APIErrorPayload {
  error?: {
    code?: string
    message?: string
  }
}

export const apiClient = axios.create({
  baseURL: '/api/v1',
  timeout: 15_000,
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

export function getAPIErrorMessage(error: unknown, fallback = '操作失败，请稍后重试') {
  if (axios.isAxiosError<APIErrorPayload>(error)) {
    return error.response?.data?.error?.message ?? fallback
  }
  return fallback
}

// Download endpoints use responseType: 'blob', including when the server returns
// a JSON error body. Decode that body so export limits remain actionable in the UI.
export async function getDownloadAPIError(error: unknown, fallback = '操作失败，请稍后重试') {
  if (!axios.isAxiosError<APIErrorPayload | Blob>(error)) return { message: fallback }

  const body = error.response?.data
  if (typeof Blob !== 'undefined' && body instanceof Blob) {
    try {
      const text = typeof body.text === 'function' ? await body.text() : await readBlobWithFileReader(body)
      const decoded = JSON.parse(text) as APIErrorPayload
      return { code: decoded.error?.code, message: decoded.error?.message ?? fallback }
    } catch {
      return { message: fallback }
    }
  }
  const decoded = body as APIErrorPayload | undefined
  return { code: decoded?.error?.code, message: decoded?.error?.message ?? fallback }
}

export async function getDownloadAPIErrorMessage(error: unknown, fallback = '操作失败，请稍后重试') {
  return (await getDownloadAPIError(error, fallback)).message
}

function readBlobWithFileReader(blob: Blob) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => reject(reader.error)
    reader.readAsText(blob)
  })
}
