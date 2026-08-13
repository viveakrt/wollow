// Platform-level types: things neither product owns. The AI provider config is
// stored once and shared, so it lives here rather than under products/mail.

export type AiProvider = 'none' | 'anthropic' | 'openai' | 'lmstudio' | 'custom'

export interface Settings {
  aiProvider: AiProvider
  modelName: string
  baseUrl: string
  hasApiKey: boolean
}

export interface SettingsInput {
  aiProvider: AiProvider
  modelName: string
  baseUrl: string
  apiKey: string
}

export type ProductId = 'mail' | 'money'
