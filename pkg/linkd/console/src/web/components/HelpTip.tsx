import {
  type ReactNode,
  useCallback,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";

interface HelpTipProps {
  label: string;
  children: ReactNode;
}

interface TooltipPosition {
  left: number;
  top: number;
  placement: "above" | "below";
}

// HelpTip 使用 portal 展示简短说明，避免被卡片或表格的 overflow 裁剪。
// 问号在 hover 和键盘 focus 时均可触达，不改变周围控件的行为。
export function HelpTip({ label, children }: HelpTipProps) {
  const tooltipID = useId();
  const triggerRef = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<TooltipPosition>();

  const updatePosition = useCallback(() => {
    const trigger = triggerRef.current;
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const margin = 150;
    setPosition({
      left: Math.max(
        margin,
        Math.min(window.innerWidth - margin, rect.left + rect.width / 2),
      ),
      top: rect.top < 96 ? rect.bottom : rect.top,
      placement: rect.top < 96 ? "below" : "above",
    });
  }, []);

  useLayoutEffect(() => {
    if (!open) return;
    updatePosition();
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [open, updatePosition]);

  function show() {
    updatePosition();
    setOpen(true);
  }

  return (
    <span className="help-tip">
      <span
        ref={triggerRef}
        className="help-tip-trigger"
        role="img"
        tabIndex={0}
        aria-label={`${label}说明`}
        aria-describedby={open ? tooltipID : undefined}
        onMouseEnter={show}
        onMouseLeave={() => {
          if (document.activeElement !== triggerRef.current) setOpen(false);
        }}
        onFocus={show}
        onBlur={() => setOpen(false)}
      >
        ?
      </span>
      {open &&
        position &&
        createPortal(
          <span
            id={tooltipID}
            className={`help-tip-content ${position.placement}`}
            role="tooltip"
            style={{ left: position.left, top: position.top }}
          >
            {children}
          </span>,
          document.body,
        )}
    </span>
  );
}

export function HelpLabel({
  label,
  help,
}: {
  label: ReactNode;
  help: ReactNode;
}) {
  const accessibleLabel = typeof label === "string" ? label : "字段";
  return (
    <span className="help-label">
      <span>{label}</span>
      <HelpTip label={accessibleLabel}>{help}</HelpTip>
    </span>
  );
}

export function HelpTableHeader({
  label,
  help,
}: {
  label: string;
  help: ReactNode;
}) {
  return (
    <th aria-label={label}>
      <HelpLabel label={label} help={help} />
    </th>
  );
}
