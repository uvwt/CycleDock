/**
 * 极简开双引号图标（“），供气泡内轻引用与输入框引用条共用，保证全平台基线与视觉风格一致。
 */
export function QuoteMarkIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 14"
      fill="currentColor"
      aria-hidden="true"
      className={className}
    >
      <path d="M6.4 1.2C3.1 2.2 1.1 4.9 1.1 8.4c0 2.5 1.5 4.2 3.5 4.2 1.8 0 3.1-1.3 3.1-3 0-1.6-1.1-2.8-2.6-2.9.2-1.7 1.2-3.1 2.7-3.9L6.4 1.2Zm7.5 0c-3.3 1-5.3 3.7-5.3 7.2 0 2.5 1.5 4.2 3.5 4.2 1.8 0 3.1-1.3 3.1-3 0-1.6-1.1-2.8-2.6-2.9.2-1.7 1.2-3.1 2.7-3.9L13.9 1.2Z" />
    </svg>
  );
}
