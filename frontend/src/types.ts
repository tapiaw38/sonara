export type Message = { role: 'user' | 'assistant'; text: string; time: string }
export type AudioSourceOption = {
  target: string
  label: string
  running: boolean
  meeting: boolean
}
export type SessionRow = {
  id: string
  startedAt: string
  mode: string
  language: string
  messages: number
  preview: string
  title: string
}
export type TranscriptEntry = { text: string; translation: string }
