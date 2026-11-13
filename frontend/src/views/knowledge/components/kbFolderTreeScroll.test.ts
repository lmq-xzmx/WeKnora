import assert from 'node:assert/strict'
import test from 'node:test'

import { resolveHorizontalWheel, type FolderTreeScrollState } from './kbFolderTreeScroll.ts'

const base: FolderTreeScrollState = {
  canScrollX: true,
  canScrollY: false,
  scrollLeft: 0,
  maxScrollLeft: 300,
  scrollTop: 0,
  remainingY: 0,
}

test('触控板横滑（deltaX≠0）交给浏览器原生处理', () => {
  assert.equal(resolveHorizontalWheel(base, 40, 0), null)
  assert.equal(resolveHorizontalWheel(base, 40, 20), null)
})

test('横向不可滚时不接管', () => {
  assert.equal(resolveHorizontalWheel({ ...base, canScrollX: false }, 0, 20), null)
})

test('侧栏不能纵向滚时，滚轮直接当横轴用', () => {
  assert.equal(resolveHorizontalWheel(base, 0, 60), 60)
})

test('侧栏能纵向滚且不在尽头时，滚轮留给纵向列表', () => {
  const state: FolderTreeScrollState = { ...base, canScrollY: true, scrollTop: 20, remainingY: 100 }
  assert.equal(resolveHorizontalWheel(state, 0, 60), null)
})

test('侧栏能纵向滚且已到底部时，向下滚转成横向', () => {
  const state: FolderTreeScrollState = { ...base, canScrollY: true, scrollTop: 200, remainingY: 0 }
  assert.equal(resolveHorizontalWheel(state, 0, 60), 60)
})

test('侧栏能纵向滚且已到顶部时，向上滚转成横向', () => {
  const state: FolderTreeScrollState = { ...base, canScrollY: true, scrollTop: 0, remainingY: 120 }
  // 横向已经在最左端，往上滚没得滚 → 不拦截，事件还给浏览器。
  assert.equal(resolveHorizontalWheel(state, 0, -60), null)
  assert.equal(resolveHorizontalWheel({ ...state, scrollLeft: 100 }, 0, -60), 40)
})

test('横向滚动被夹在 [0, maxScrollLeft] 之间', () => {
  assert.equal(resolveHorizontalWheel({ ...base, scrollLeft: 280 }, 0, 60), 300)
  assert.equal(resolveHorizontalWheel({ ...base, scrollLeft: 0 }, 0, -60), null)
})

test('横向已经到头时不再拦截事件', () => {
  assert.equal(resolveHorizontalWheel({ ...base, scrollLeft: 300 }, 0, 60), null)
  assert.equal(resolveHorizontalWheel({ ...base, scrollLeft: 0 }, 0, -60), null)
})
