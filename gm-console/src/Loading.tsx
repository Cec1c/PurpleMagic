import { useEffect, useState } from "react";
import { LoaderCircle } from "lucide-react";

export function DelayedSpinner({ active, size = 18 }: { active: boolean; size?: number }) {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    if (!active) { setVisible(false); return; }
    const timer = setTimeout(() => setVisible(true), 120);
    return () => clearTimeout(timer);
  }, [active]);
  return <span className="query-spinner" data-visible={active && visible} aria-hidden="true">
    <LoaderCircle size={size} className={active && visible ? "is-spinning" : undefined} />
  </span>;
}

export function LoadingSkeleton({ rows = 6 }: { rows?: number }) {
  return <div className="query-skeleton" role="status" aria-label="正在读取">
    {Array.from({ length: rows }, (_, i) => <div className="query-skeleton-row" key={i} aria-hidden="true">
      <i /><i /><i />
    </div>)}
  </div>;
}
