/**
 * 将 Markdown 文本转换为适合列表/卡片摘要展示的纯文本，
 * 避免在截断预览中暴露反引号、井号、星号或未闭合代码块等原始符号。
 */
export function stripMarkdownSummary(input?: string | null): string {
  if (!input) return "";

  return input
    // 移除围栏代码块标记，保留内部可读文本
    .replace(/```[\w-]*\n?([\s\S]*?)```/g, "$1")
    // 图片语法 ![alt](url) -> alt
    .replace(/!\[([^\]]*)\]\([^)]+\)/g, "$1")
    // 链接语法 [text](url) -> text
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    // 行内代码 `code` -> code
    .replace(/`([^`]+)`/g, "$1")
    // 粗体与斜体
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/__([^_]+)__/g, "$1")
    .replace(/(^|[\s(])\*([^*\n]+)\*(?=[\s).,!?:;]|$)/g, "$1$2")
    .replace(/(^|[\s(])_([^_\n]+)_(?=[\s).,!?:;]|$)/g, "$1$2")
    // 删除线 ~~text~~ -> text
    .replace(/~~([^~]+)~~/g, "$1")
    // 标题 # Heading -> Heading
    .replace(/^\s*#{1,6}\s+/gm, "")
    // 引用 > quote -> quote
    .replace(/^\s*>\s?/gm, "")
    // 任务列表与无序列表标记
    .replace(/^\s*[-*+]\s+\[[ xX]\]\s+/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    // 水平分割线
    .replace(/^\s*(?:[-*_]\s*){3,}$/gm, "")
    // 去除残留的零散反引号
    .replace(/`/g, "")
    // 合并多余空行与空白，形成紧凑摘要
    .replace(/\r?\n+/g, " ")
    .replace(/\s{2,}/g, " ")
    .trim();
}
