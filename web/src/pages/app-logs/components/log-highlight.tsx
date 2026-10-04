export function LogHighlight({ text, keyword }: { text: string; keyword: string }) {
  if (!keyword) return text
  return text.split(keyword).map((part, index) => (
    <span key={index}>
      {index > 0 ? <mark className="rounded-sm bg-amber-300/25 text-inherit">{keyword}</mark> : null}
      {part}
    </span>
  ))
}
