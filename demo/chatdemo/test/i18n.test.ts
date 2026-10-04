import assert from 'node:assert/strict'
import test from 'node:test'

import { detectLocale, formatConversationTime, messages, translate } from '../src/i18n.ts'

test('explicit supported languages override browser preferences for shared demo links', () => {
  assert.equal(detectLocale({ languages: ['zh-CN', 'en-US'] }, 'en'), 'en')
  assert.equal(detectLocale({ languages: ['en-US', 'zh-CN'] }, 'zh'), 'zh')
  assert.equal(detectLocale(undefined, 'zh'), 'zh')
})

test('missing or unsupported URL languages preserve browser language selection', () => {
  for (const value of [undefined, null, '', 'fr', 'en-US', '<script>']) {
    assert.equal(detectLocale({ languages: ['zh-CN'] }, value), 'zh')
    assert.equal(detectLocale({ languages: ['en-US'] }, value), 'en')
  }
})

test('browser preferences select the first supported language and normalize regional tags', () => {
  for (const tag of ['zh', 'zh-CN', 'zh-TW', 'zh-HK', 'zh-Hant', 'ZH_cn']) {
    assert.equal(detectLocale({ languages: [tag, 'en-US'] }), 'zh')
  }
  for (const tag of ['en', 'en-US', 'en-GB', 'EN_us']) {
    assert.equal(detectLocale({ languages: [tag, 'zh-CN'] }), 'en')
  }
  assert.equal(detectLocale({ languages: ['fr-FR', 'zh-CN', 'en-US'] }), 'zh')
  assert.equal(detectLocale({ languages: ['en-US'], language: 'zh-CN' }), 'en')
})

test('language detection handles browsers without a preference list and falls back to English', () => {
  assert.equal(detectLocale({ language: 'zh-CN' }), 'zh')
  assert.equal(detectLocale({ languages: [], language: 'zh-TW' }), 'zh')
  assert.equal(detectLocale({ languages: ['fr-FR', 'ja-JP'] }), 'en')
  assert.equal(detectLocale({ languages: ['zhx', 'english', ''] }), 'en')
  assert.equal(detectLocale({}), 'en')
  assert.equal(detectLocale(), 'en')
})

test('both catalogs contain the same nonempty messages and interpolation parameters', () => {
  assert.deepEqual(Object.keys(messages.zh).sort(), Object.keys(messages.en).sort())
  for (const key of Object.keys(messages.en) as (keyof typeof messages.en)[]) {
    const en = messages.en[key]
    const zh = messages.zh[key]
    assert.ok(en.trim(), key)
    assert.ok(zh.trim(), key)
    assert.deepEqual(en.match(/\{\w+\}/g)?.sort(), zh.match(/\{\w+\}/g)?.sort(), key)
    assert.doesNotMatch(en, /\p{Script=Han}/u, key)
  }
})

test('translation preserves interpolated user content and numeric values', () => {
  assert.equal(translate('zh', 'connectedNode', { uid: 'alice', node: 0 }), 'alice(连接成功-节点:0)')
  assert.equal(translate('en', 'connectedNode', { uid: '用户{node}', node: 2 }), '用户{node} (Connected - Node: 2)')
  assert.equal(translate('en', 'orderQuantity', { count: 1 }), 'Quantity: 1')
  assert.equal(translate('zh', 'orderQuantity', { count: 2 }), '共2件')
})

test('conversation times localize relative labels across calendar boundaries', () => {
  const now = new Date(2026, 0, 1, 0, 10)
  const timestamp = (year: number, month: number, day: number, hour: number, minute: number) =>
    new Date(year, month - 1, day, hour, minute).getTime()
  assert.equal(formatConversationTime(now.getTime() - 30_000, 'en', now), 'Just now')
  assert.equal(formatConversationTime(now.getTime() - 30_000, 'zh', now), '刚刚')
  assert.equal(formatConversationTime(timestamp(2026, 1, 1, 0, 5), 'en', now), '00:05')
  assert.equal(formatConversationTime(timestamp(2025, 12, 31, 23, 30), 'en', now), 'Yesterday 23:30')
  assert.equal(formatConversationTime(timestamp(2025, 12, 31, 23, 30), 'zh', now), '昨天 23:30')
  assert.equal(formatConversationTime(timestamp(2025, 12, 30, 10, 0), 'en', now), '2 days ago 10:00')
  assert.equal(formatConversationTime(timestamp(2025, 12, 30, 10, 0), 'zh', now), '前天 10:00')
  assert.equal(formatConversationTime(timestamp(2025, 12, 29, 10, 0), 'en', now), 'Monday 10:00')
  assert.equal(formatConversationTime(timestamp(2025, 12, 29, 10, 0), 'zh', now), '星期一 10:00')
  assert.equal(formatConversationTime(timestamp(2025, 10, 10, 10, 0), 'en', now), '10/10/2025 10:00')
  assert.equal(formatConversationTime(timestamp(2025, 10, 10, 10, 0), 'zh', now), '2025/10/10 10:00')
})
