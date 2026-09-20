import { HelpLabel } from "./HelpTip";

export function JsonViewer({
  value,
  description = "字段保持来源系统的原始命名；未展示的字段不会被推断为默认值。",
}: {
  value: unknown;
  description?: string;
}) {
  const json = JSON.stringify(value, null, 2);
  return (
    <div className="json-viewer">
      <div className="json-viewer-toolbar">
        <HelpLabel label="JSON 字段" help={description} />
        <button
          className="copy-button"
          type="button"
          onClick={() => void navigator.clipboard.writeText(json)}
        >
          复制 JSON
        </button>
      </div>
      <pre>{json}</pre>
    </div>
  );
}
