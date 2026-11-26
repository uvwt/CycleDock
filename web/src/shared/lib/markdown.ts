/**
 * 将 Markdown 文本转换为适合列表/卡片摘要展示的纯文本，
 * 避免在截断预览中暴露反引号、井号、星号或未闭合代码块等原始符号。
 */
export function stripMarkdownSummary(input?: string | null): string {
  if (!input) return "";

  return input
    // 移除围栏代码块标记，保留内部可读文本
    .replace(/```[\w-]*\r?\n?([\s\S]*?)```/g, "$1")
    // 兜底移除未闭合的代码块起始围栏行
    .replace(/^\s*```[\w-]*\s*$/gm, "")
    // 图片语法 ![alt](url) -> alt
    .replace(/!\[([^\]]*)\]\([^)]+\)/g, "$1")
    // 链接语法 [text](url) -> text
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    // 行内代码 `code` -> code
    .replace(/`([^`]+)`/g, "$1")
    // 粗体与斜体
    .replace(/\*\*\*([^*]+)\*\*\*/g, "$1")
    .replace(/___([^_]+)___/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/__([^_]+)__/g, "$1")
    .replace(/(^|[\s(])\*([^*\n]+)\*(?=[\s).,!?:;]|$)/g, "$1$2")
    .replace(/(^|[\s(])_([^_\n]+)_(?=[\s).,!?:;]|$)/g, "$1$2")
    // 删除线 ~~text~~ -> text
    .replace(/~~([^~]+)~~/g, "$1")
    // 标题 # Heading -> Heading（含行尾闭合井号）
    .replace(/^\s*#{1,6}\s+/gm, "")
    .replace(/\s+#{1,6}\s*$/gm, "")
    // 引用 > quote -> quote（支持嵌套引用）
    .replace(/^\s*(?:>\s?)+/gm, "")
    // 任务列表、无序列表与有序列表前缀
    .replace(/^\s*[-*+]\s+\[[ xX]\]\s+/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    .replace(/^\s*\d+[.)]\s+/gm, "")
    // 水平分割线
    .replace(/^\s*(?:[-*_]\s*){3,}$/gm, "")
    // 去除残留的零散反引号
    .replace(/`/g, "")
    // 合并多余空行与空白，形成紧凑摘要
    .replace(/\r?\n+/g, " ")
    .replace(/\s{2,}/g, " ")
    .trim();
}
