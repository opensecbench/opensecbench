import { Providers } from './Providers'

// ProviderSettings is the Analyst dock's connection picker: select which connection the chat uses. It is
// deliberately minimal — adding/configuring connections, tags, approvals, and custom agents all live in
// the global Settings surface, not here.
export function ProviderSettings({
  online,
  onClose,
  onChanged,
}: {
  online: boolean
  onClose: () => void
  onChanged: () => void
}) {
  return (
    <div className="prov">
      <div className="prov-head">
        Use a connection
        <button className="link" onClick={onClose}>done</button>
      </div>
      <div className="prov-scroll">
        <Providers online={online} onChanged={onChanged} manage={false} />
        <div className="prov-hint">Add or configure connections, tags, approvals and agents in Settings → Models &amp; Providers.</div>
      </div>
    </div>
  )
}
