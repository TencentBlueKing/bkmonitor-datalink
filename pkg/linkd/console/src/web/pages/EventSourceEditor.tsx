import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import {
  editableSpec,
  formToSpec,
  parseSourceJSON,
  sourcePath,
  sourceRecordSchema,
  sourceRequest,
  SourceRequestError,
  SourceValidationError,
  sourceRoles,
  specToForm,
  validateSource,
  type SourceForm,
  type SourceRecord,
  type SourceRole,
  type SourceSpec,
} from "./event-sources";

function SourceModal({
  title,
  children,
  footer,
  onClose,
  alert = false,
  inactive = false,
}: {
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
  alert?: boolean;
  inactive?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const overflow = document.body.style.overflow;
    const shell = document.querySelector<HTMLElement>(".app-shell");
    const wasInert = shell?.inert ?? false;
    if (shell) shell.inert = true;
    document.body.style.overflow = "hidden";
    (
      ref.current?.querySelector<HTMLElement>("[data-autofocus]") ?? ref.current
    )?.focus();
    return () => {
      document.body.style.overflow = overflow;
      if (shell) shell.inert = wasInert;
      if (previous?.isConnected) previous.focus();
    };
  }, []);
  return createPortal(
    <div
      className={`event-source-modal-layer ${alert ? "event-source-confirm-layer" : ""}`}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={ref}
        className={
          alert
            ? "event-source-confirm event-source-surface"
            : "event-source-drawer event-source-surface"
        }
        role={alert ? "alertdialog" : "dialog"}
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        inert={inactive}
        onKeyDown={(e) => {
          if (inactive) return;
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            onClose();
          }
          if (e.key !== "Tab") return;
          const controls = Array.from(
            e.currentTarget.querySelectorAll<HTMLElement>(
              'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]',
            ),
          );
          const first = controls[0],
            last = controls.at(-1);
          if (!first) {
            e.preventDefault();
            return;
          }
          if (
            e.shiftKey &&
            (document.activeElement === first ||
              document.activeElement === e.currentTarget)
          ) {
            e.preventDefault();
            last?.focus();
          } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
          }
        }}
      >
        <header>
          <div>
            <p className="eyebrow">EVENT SOURCE</p>
            <h2>{title}</h2>
          </div>
          <button
            type="button"
            aria-label={alert ? "关闭确认" : "关闭来源详情"}
            onClick={onClose}
            data-autofocus={alert ? undefined : true}
          >
            ×
          </button>
        </header>
        <div className="event-source-drawer-body">{children}</div>
        {footer && <footer>{footer}</footer>}
      </div>
    </div>,
    document.body,
  );
}

function SourceField({
  label,
  name,
  errorField,
  children,
}: {
  label: string;
  name: string;
  errorField?: string;
  children: ReactNode;
}) {
  return (
    <label
      className={`event-source-field ${errorField === name ? "invalid" : ""}`}
    >
      <span>{label}</span>
      {children}
      {errorField === name && (
        <span className="event-source-field-error">
          请检查此项，具体原因见上方提示
        </span>
      )}
    </label>
  );
}

function SourceFormFields({
  form,
  setForm,
  existing,
  spec,
  errorField,
}: {
  form: SourceForm;
  setForm: (form: SourceForm) => void;
  existing: boolean;
  spec: SourceSpec;
  errorField?: string;
}) {
  const field = (
    label: string,
    key: "id" | "tenant" | "topic" | "group",
    path: string,
  ) => (
    <SourceField label={label} name={path} errorField={errorField}>
      <input
        aria-label={label}
        value={form[key]}
        readOnly={existing}
        aria-invalid={errorField === path}
        onChange={(e) => setForm({ ...form, [key]: e.target.value })}
      />
    </SourceField>
  );
  function placement(role: SourceRole) {
    const value = form[role];
    const label = role === "cleaner" ? "Cleaner" : "Lifecycle";
    const update = (next: typeof value) => setForm({ ...form, [role]: next });
    return (
      <section className="event-source-form-section" key={role}>
        <h3>{label} 调度</h3>
        <SourceField
          label={`${label} 副本数`}
          name={`scheduling.${role}.replicas`}
          errorField={errorField}
        >
          <input
            aria-label={`${label} 副本数`}
            value={value.replicas}
            aria-invalid={errorField === `scheduling.${role}.replicas`}
            onChange={(e) => update({ ...value, replicas: e.target.value })}
          />
        </SourceField>
        <p className="event-source-hint">
          all 使用全部匹配 Worker；0 停止该角色。
          {role === "cleaner" && "Cleaner 数量还受 Kafka 分区数限制。"}
        </p>
        <div className="event-source-selector-heading">
          <h4>{label} selector</h4>
          <button
            type="button"
            disabled={value.selector.length >= 32}
            onClick={() =>
              update({
                ...value,
                selector: [...value.selector, { key: "", value: "" }],
              })
            }
          >
            添加 {label} 标签
          </button>
        </div>
        {value.selector.map((row, index) => (
          <div className="event-source-selector-row" key={index}>
            <SourceField
              label="标签键"
              name={`scheduling.${role}.selector`}
              errorField={errorField}
            >
              <input
                aria-label={`${label} 标签键 ${index + 1}`}
                value={row.key}
                onChange={(e) =>
                  update({
                    ...value,
                    selector: value.selector.map((item, i) =>
                      i === index ? { ...item, key: e.target.value } : item,
                    ),
                  })
                }
              />
            </SourceField>
            <SourceField label="标签值" name={`scheduling.${role}.selector`}>
              <input
                aria-label={`${label} 标签值 ${index + 1}`}
                value={row.value}
                onChange={(e) =>
                  update({
                    ...value,
                    selector: value.selector.map((item, i) =>
                      i === index ? { ...item, value: e.target.value } : item,
                    ),
                  })
                }
              />
            </SourceField>
            <button
              type="button"
              aria-label={`移除 ${label} 标签 ${index + 1}`}
              onClick={() =>
                update({
                  ...value,
                  selector: value.selector.filter((_, i) => i !== index),
                })
              }
            >
              移除
            </button>
          </div>
        ))}
        <p className="event-source-hint">
          标签必须全部精确匹配。空 selector 不匹配要求显式标签的 Worker。
        </p>
      </section>
    );
  }
  return (
    <>
      {existing && (
        <p className="event-source-hint">
          身份与订阅字段为只读；需要变更时请新建来源。
        </p>
      )}
      <section className="event-source-form-section">
        <h3>基本信息</h3>
        <div className="event-source-form-grid">
          {field("来源 ID", "id", "event_source_id")}
          {field("关联租户", "tenant", "related_tenant_id")}
          <label className="event-source-checkbox">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            启用来源
          </label>
        </div>
      </section>
      <section className="event-source-form-section">
        <h3>Kafka 输入</h3>
        <SourceField
          label="Brokers（每行一个）"
          name="storage.kafka.brokers"
          errorField={errorField}
        >
          <textarea
            aria-label="Brokers（每行一个）"
            rows={3}
            value={form.brokers}
            readOnly={existing}
            aria-invalid={errorField === "storage.kafka.brokers"}
            onChange={(e) => setForm({ ...form, brokers: e.target.value })}
          />
        </SourceField>
        <div className="event-source-form-grid">
          {field("Topic", "topic", "storage.kafka.topic")}
          {field("Consumer group", "group", "storage.kafka.consumer_group")}
        </div>
        <p className="event-source-hint">
          {existing
            ? "未提交输入 Kafka security 时保留原凭据；仅在轮换时通过高级 JSON 填写完整认证配置。"
            : "需要认证时，可在高级 JSON 中填写 Kafka security。"}
        </p>
      </section>
      {sourceRoles.map(placement)}
      <details className="event-source-advanced-summary">
        <summary>Cleaner、指纹与高级配置</summary>
        <p>
          Cleaner 类型：{spec.cleaner?.type || "standard"}；指纹模式：
          {spec.fingerprint_mode || "field"}
        </p>
        <p>
          指纹字段：
          {spec.fingerprint_field ||
            spec.fingerprint_fields?.join(", ") ||
            "source_alert_id"}
        </p>
        <p className="event-source-hint">
          Cleaner 预算、Enrich、Hooks 和其他完整配置在高级 JSON
          中编辑，切换编辑方式会保留这些字段。
        </p>
      </details>
    </>
  );
}

export function EventSourceEditor({
  initial,
  runtime,
  latestRevision,
  onClose,
  onSaved,
}: {
  initial?: SourceRecord;
  runtime: ReactNode;
  latestRevision?: number;
  onClose: () => void;
  onSaved: (record: SourceRecord) => void;
}) {
  const [source, setSource] = useState(initial);
  const [base, setBase] = useState(() => editableSpec(initial));
  const [draft, setDraft] = useState(base);
  const [form, setForm] = useState(() => specToForm(base));
  const [text, setText] = useState(() => JSON.stringify(base, null, 2));
  const [mode, setMode] = useState<"form" | "json">("form");
  const [tab, setTab] = useState<"config" | "runtime">("config");
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState<{
    message: string;
    field?: string;
    conflict?: boolean;
  }>();
  const feedback = useRef<HTMLDivElement>(null);
  const [success, setSuccess] = useState("");
  const [confirmation, setConfirmation] = useState<
    "discard" | "delete" | "reload"
  >();
  useEffect(() => {
    if (error) feedback.current?.focus();
  }, [error]);
  // 自动刷新只更新外部快照。编辑的基准 revision 仅在成功写入或用户主动重载后变化。
  const dirty =
    mode === "json"
      ? text !== JSON.stringify(base, null, 2)
      : JSON.stringify(draft) !== JSON.stringify(base) ||
        JSON.stringify(form) !== JSON.stringify(specToForm(draft));
  useEffect(() => {
    if (!dirty) return;
    const prevent = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener("beforeunload", prevent);
    return () => window.removeEventListener("beforeunload", prevent);
  }, [dirty]);
  function report(cause: unknown) {
    setSuccess("");
    setError({
      message: cause instanceof Error ? cause.message : "操作失败，请重试",
      field: cause instanceof SourceValidationError ? cause.field : undefined,
      conflict: cause instanceof SourceRequestError && cause.status === 409,
    });
  }
  function reset(record: SourceRecord) {
    const next = editableSpec(record);
    setSource(record);
    setBase(next);
    setDraft(next);
    setForm(specToForm(next));
    setText(JSON.stringify(next, null, 2));
    setError(undefined);
  }
  function close() {
    if (submitting.current) return;
    if (dirty) setConfirmation("discard");
    else onClose();
  }
  function switchMode(next: "form" | "json") {
    if (next === mode) return;
    try {
      const spec =
        mode === "form" ? formToSpec(form, draft) : parseSourceJSON(text);
      setDraft(spec);
      setText(JSON.stringify(spec, null, 2));
      setForm(specToForm(spec));
      setMode(next);
      setError(undefined);
    } catch (cause) {
      report(cause);
    }
  }
  async function save(remove = false) {
    if (submitting.current || (remove && !source)) return;
    submitting.current = true;
    setBusy(true);
    setError(undefined);
    setSuccess("");
    try {
      // 删除只使用选中记录的身份与版本，不能被无效 JSON 或草稿中的 ID 改写。
      const spec = remove
        ? undefined
        : mode === "json"
          ? parseSourceJSON(text)
          : formToSpec(form, draft);
      if (spec) validateSource(spec, source);
      const record = sourceRecordSchema.parse(
        await sourceRequest(
          sourcePath(source?.id ?? spec!.event_source_id),
          remove ? "DELETE" : "PUT",
          {
            expected_revision: source?.revision ?? 0,
            ...(spec ? { spec } : {}),
          },
        ),
      );
      reset(record);
      onSaved(record);
      const published =
        record.published === record.revision && record.published > 0;
      setSuccess(
        published
          ? `${remove ? "来源已删除" : "配置已发布"} · 版本 ${record.published}。实际任务状态请查看运行情况。`
          : `配置已接收 · 编辑版本 ${record.revision}，当前发布版本 ${record.published}，等待发布完成。`,
      );
    } catch (cause) {
      report(cause);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }
  async function reload() {
    if (!source || submitting.current) return;
    submitting.current = true;
    setBusy(true);
    try {
      const record = sourceRecordSchema.parse(
        await sourceRequest(sourcePath(source.id)),
      );
      reset(record);
      onSaved(record);
      setSuccess("已载入最新配置");
    } catch (cause) {
      report(cause);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }
  const title = source ? `来源 ${source.id}` : "新增来源";
  return (
    <>
      <SourceModal
        title={title}
        onClose={close}
        inactive={Boolean(confirmation)}
        footer={
          <>
            <div className="event-source-footer-meta">
              {dirty ? "有未保存的修改" : "无未保存的修改"}
              {source &&
                ` · 编辑版本 ${source.revision} / 发布版本 ${source.published}`}
            </div>
            <div className="event-source-footer-actions">
              {source && !source.deleted && (
                <button
                  type="button"
                  className="event-source-danger"
                  disabled={busy}
                  onClick={() => setConfirmation("delete")}
                >
                  删除来源
                </button>
              )}
              {source && (
                <button
                  type="button"
                  disabled={busy}
                  onClick={() =>
                    dirty ? setConfirmation("reload") : void reload()
                  }
                >
                  重新载入
                </button>
              )}
              <span className="event-source-action-spacer" />
              <button type="button" disabled={busy} onClick={close}>
                取消
              </button>
              <button
                type="button"
                className="primary-button"
                disabled={busy || (!!source && !source.deleted && !dirty)}
                onClick={() => void save()}
              >
                {busy
                  ? "处理中…"
                  : source?.deleted
                    ? "恢复并发布"
                    : "保存并发布"}
              </button>
            </div>
          </>
        }
      >
        <div className="event-source-tabs" role="tablist" aria-label="来源详情">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "config"}
            aria-controls="source-config-panel"
            id="source-config-tab"
            onClick={() => setTab("config")}
          >
            配置
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "runtime"}
            aria-controls="source-runtime-panel"
            id="source-runtime-tab"
            disabled={!source}
            onClick={() => setTab("runtime")}
          >
            运行情况
          </button>
        </div>
        {success && (
          <div role="status" className="event-source-success">
            {success}
          </div>
        )}
        {error && (
          <div
            ref={feedback}
            tabIndex={-1}
            role="alert"
            className="error-banner"
            id="source-edit-error"
          >
            <strong>{error.message}</strong>
            {error.field && <div>位置：{error.field}</div>}
            {error.conflict && (
              <p>草稿已保留。请点击“重新载入”查看最新配置后再编辑发布。</p>
            )}
          </div>
        )}
        {source &&
          latestRevision !== undefined &&
          latestRevision > source.revision && (
            <div className="warning-banner">
              来源已有新版本，当前草稿仍基于版本 {source.revision}
              ；发布前请重新载入。
            </div>
          )}
        {source?.deleted && (
          <div className="warning-banner">
            此来源已删除。恢复后按当前启停配置运行；如需启动，请勾选“启用来源”。
          </div>
        )}
        {tab === "runtime" ? (
          <div
            id="source-runtime-panel"
            role="tabpanel"
            aria-labelledby="source-runtime-tab"
          >
            {runtime}
          </div>
        ) : (
          <div
            id="source-config-panel"
            role="tabpanel"
            aria-labelledby="source-config-tab"
          >
            <fieldset disabled={busy} className="event-source-editor-fields">
              <div
                className="event-source-tabs"
                role="tablist"
                aria-label="编辑方式"
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={mode === "form"}
                  aria-controls="source-editor-panel"
                  id="source-form-tab"
                  onClick={() => switchMode("form")}
                >
                  常用表单
                </button>
                <button
                  type="button"
                  role="tab"
                  aria-selected={mode === "json"}
                  aria-controls="source-editor-panel"
                  id="source-json-tab"
                  onClick={() => switchMode("json")}
                >
                  高级 JSON
                </button>
              </div>
              <div
                id="source-editor-panel"
                role="tabpanel"
                aria-labelledby={
                  mode === "form" ? "source-form-tab" : "source-json-tab"
                }
              >
                {mode === "form" ? (
                  <SourceFormFields
                    form={form}
                    setForm={setForm}
                    existing={Boolean(source)}
                    spec={draft}
                    errorField={error?.field}
                  />
                ) : (
                  <>
                    <div className="event-source-json-toolbar">
                      <label htmlFor="source-spec">EventSource 配置</label>
                      <button
                        type="button"
                        onClick={() => {
                          try {
                            setText(
                              JSON.stringify(parseSourceJSON(text), null, 2),
                            );
                            setError(undefined);
                          } catch (cause) {
                            report(cause);
                          }
                        }}
                      >
                        格式化 JSON
                      </button>
                    </div>
                    <textarea
                      id="source-spec"
                      className="event-source-json"
                      rows={25}
                      value={text}
                      spellCheck={false}
                      aria-invalid={Boolean(error?.field)}
                      aria-describedby={error ? "source-edit-error" : undefined}
                      onChange={(e) => setText(e.target.value)}
                    />
                    <p className="event-source-hint">
                      完整配置会保留。输入 Kafka security
                      省略时沿用原凭据；Hooks 与 Enrich
                      的已有脱敏占位符由控制面按原配置身份处理。新增或更换连接时请填写有效凭据。
                    </p>
                  </>
                )}
              </div>
            </fieldset>
          </div>
        )}
      </SourceModal>
      {confirmation && (
        <SourceModal
          title={
            confirmation === "delete"
              ? "确认删除来源"
              : confirmation === "reload"
                ? "重新载入配置"
                : "放弃未保存的修改"
          }
          alert
          onClose={() => setConfirmation(undefined)}
          footer={
            <>
              <button
                type="button"
                data-autofocus
                onClick={() => setConfirmation(undefined)}
              >
                继续编辑
              </button>
              <button
                type="button"
                className={
                  confirmation === "delete"
                    ? "event-source-danger"
                    : "primary-button"
                }
                onClick={() => {
                  const action = confirmation;
                  setConfirmation(undefined);
                  if (action === "discard") onClose();
                  else if (action === "delete") void save(true);
                  else void reload();
                }}
              >
                {confirmation === "delete"
                  ? "确认删除"
                  : confirmation === "reload"
                    ? "放弃修改并载入"
                    : "放弃修改"}
              </button>
            </>
          }
        >
          <p>
            {confirmation === "delete"
              ? `将删除来源 ${source?.id} 并停止其调度。此操作不会删除已有 Event 和 Alert，来源可恢复。`
              : "未保存的修改会丢失，是否继续？"}
          </p>
        </SourceModal>
      )}
    </>
  );
}
