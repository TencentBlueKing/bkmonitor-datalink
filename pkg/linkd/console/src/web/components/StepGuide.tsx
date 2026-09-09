export interface StepGuide {
  summary: string;
  success: string;
  boundary: string;
}

// ProcessingFlowHeader 用统一的标题与概览说明模块级处理语义；具体节点语义由 StepGuideCard 承担。
export function ProcessingFlowHeader({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <header className="processing-flow-header">
      <div>
        <p className="eyebrow">PROCESSING FLOW</p>
        <h2>{title}</h2>
      </div>
      <div className="processing-flow-overview" aria-label="模块运行说明">
        <strong>模块如何运作</strong>
        <span>{description}</span>
      </div>
    </header>
  );
}

// StepGuideCard 解释流程节点的可观察语义，不把当前快照或指标结果重复成说明。
export function StepGuideCard({
  step,
  guide,
}: {
  step: string;
  guide: StepGuide;
}) {
  return (
    <section className="step-guide" aria-label={`${step} 步骤说明`}>
      <header>
        <p className="eyebrow">STEP GUIDE</p>
        <h3>步骤说明</h3>
      </header>
      <p>{guide.summary}</p>
      <dl>
        <div>
          <dt>成功表示</dt>
          <dd>{guide.success}</dd>
        </div>
        <div>
          <dt>关键边界</dt>
          <dd>{guide.boundary}</dd>
        </div>
      </dl>
    </section>
  );
}
