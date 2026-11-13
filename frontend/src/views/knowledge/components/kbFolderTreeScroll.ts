/**
 * 文件夹树的横向滚动换算。
 *
 * 背景：侧栏只有 220px 宽，文件夹名比它长时以前是直接省略号截断，看不全。
 * 现在改成「整棵树按最长的名字撑开 + 横向滚动查看」，于是多了一个问题：
 * 纯鼠标滚轮（只有 deltaY）停在树上时，该滚列表还是该滚横轴？
 *
 * 规则（与 KB 页布局配套：页面本身不滚，滚动都发生在内部面板里）：
 * - 触控板横滑（deltaX ≠ 0）交还浏览器原生处理，不插手；
 * - 侧栏自己不能纵向滚时（常见：文件夹不多），滚轮直接当横轴用；
 * - 侧栏能纵向滚时，只有已经滚到纵向尽头、还继续同向滚，才把余量转成横向，
 *   这样纵向浏览不会被抢走。
 *
 * 纯函数，不依赖 DOM，方便单测。
 */

/** 换算所需的最小状态快照。 */
export interface FolderTreeScrollState {
  /** 横向是否可滚（scrollWidth > clientWidth）。 */
  canScrollX: boolean
  /** 纵向是否可滚（scrollHeight > clientHeight）。 */
  canScrollY: boolean
  /** 当前横向滚动位置。 */
  scrollLeft: number
  /** 横向可滚动的总距离。 */
  maxScrollLeft: number
  /** 当前纵向滚动位置。 */
  scrollTop: number
  /** 纵向剩余可滚动距离（scrollHeight - clientHeight - scrollTop）。 */
  remainingY: number
}

/**
 * 把滚轮事件换算成新的横向滚动位置。
 *
 * @returns 新的 scrollLeft；返回 null 表示「这件事交给浏览器原生处理」。
 */
export function resolveHorizontalWheel(
  state: FolderTreeScrollState,
  deltaX: number,
  deltaY: number,
): number | null {
  // 触控板横滑：浏览器原生支持，别插手。
  if (deltaX !== 0) return null
  if (!state.canScrollX || deltaY === 0) return null

  if (state.canScrollY) {
    // 纵向还滚得动时，滚轮优先给纵向列表；只有到了尽头才转横向。
    const movingUp = deltaY < 0
    if (movingUp ? state.scrollTop > 0 : state.remainingY > 0) return null
  }

  const next = Math.min(Math.max(state.scrollLeft + deltaY, 0), state.maxScrollLeft)
  // 已经到头了就不用拦事件，让浏览器继续处理（页面滚动等）。
  if (next === state.scrollLeft) return null
  return next
}
