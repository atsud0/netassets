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
