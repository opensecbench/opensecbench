import { ApprovalPolicy } from './ApprovalPolicy'
import { CustomAgents } from './CustomAgents'
import { Providers } from './Providers'

// ProviderSettings is the Analyst dock's inline config overlay: providers + usage, the approval policy,
// and custom agents. The same leaves are reused as tabs in the global Settings surface (ADR-0021).
export function ProviderSettings({
  online,
  projectId,
  onClose,
  onChanged,
}: {
  online: boolean
  projectId: string
  onClose: () => void
  onChanged: () => void
}) {
  return (
    <div className="prov">
      <div className="prov-head">
        Use a connection
        <button className="link" onClick={onClose}>done</button>
      </div>
      <Providers online={online} projectId={projectId} onChanged={onChanged} manage={false} />
      <ApprovalPolicy online={online} />
      <CustomAgents online={online} />
    </div>
  )
}
