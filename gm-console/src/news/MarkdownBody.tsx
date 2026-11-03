import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

export function newsURL(value: string): string {
  try {
    const url = new URL(value);
    return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password ? url.href : "";
  } catch { return ""; }
}

export function MarkdownBody({body, format}: {body:string; format?:string}) {
  if (format !== "markdown") return <div className="news-plain">{body}</div>;
  return <div className="news-markdown"><Markdown remarkPlugins={[remarkGfm]} skipHtml urlTransform={newsURL}
    components={{
      a: ({node:_,...props}) => <a {...props} target="_blank" rel="noopener noreferrer" />,
      img: ({node:_,...props}) => props.src ? <img {...props} loading="lazy" referrerPolicy="no-referrer" /> : <span>{props.alt}</span>,
      table: ({node:_,...props}) => <div className="news-table"><table {...props} /></div>,
    }}>{body}</Markdown></div>;
}
