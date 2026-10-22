import { type ReactNode, useCallback, useEffect, useState } from 'react'
import {
  IconAlertTriangle,
  IconChain,
  IconCheck,
  IconCopy,
  IconDownload,
  IconKey,
  IconPlay,
  IconRefresh,
  IconShieldCheck,
} from './Icons'
import { JsonTreeView } from './JsonTreeView'
import { api } from '../lib/api'
import { downloadFile } from '../lib/export'

/* ── Types ────────────────────────────────────────────────── */

type LedgerDigest = {
  version?: number
  lsn?: number
  chain?: string
  tree_root?: string
  state_root?: string
  term?: number
  timestamp?: number
  node_id?: string
  key_id?: string
  signed_at?: string
  signature?: string
}

type LedgerStatus = {
  mode?: 'off' | 'chain' | 'signed'
  signed?: boolean
  provable?: boolean
  rows_provable?: boolean
  digest?: LedgerDigest
  explanation?: string
}

type ConsistencyProof = {
  from_lsn?: number
  to_lsn?: number
  from_digest?: unknown
  to_digest?: unknown
  proof?: unknown
}

type RowProof = {
  lsn?: number
  digest?: unknown
  rows?: unknown
  proof?: unknown
  groups?: unknown
}

type QueryAttestation = {
  attestation?: Record<string, unknown>
  rows?: unknown
}

type RowKeyDraft = {
  domain: string
  table: string
  // kind 'row' cites one row by its primary key; 'group' asks for a whole
  // verifiable group, which is what proves a complete set.
  kind: 'row' | 'group'
  pk: string
  groupColumns: string
}

type Toast = { id: number; message: string; kind: 'success' | 'error' }
let toastSeq = 0

/* ── Helpers ──────────────────────────────────────────────── */

const MODE_LABEL: Record<string, string> = {
  off: 'No digests',
  chain: 'Hash chain only',
  signed: 'Signed',
}

function shortHash(value?: string): string {
  if (!value) return '—'
  if (value.length <= 20) return value
  return `${value.slice(0, 10)}…${value.slice(-6)}`
}

function digestCoverage(digest?: LedgerDigest): string {
  if (!digest) return 'nothing'
  const covered = ['the WAL hash chain']
  if (digest.tree_root) covered.push('the log tree')
  if (digest.state_root) covered.push('the state tree')
  return covered.join(', ')
}

/* ── Main panel ───────────────────────────────────────────── */

export function LedgerPanel() {
  const [status, setStatus] = useState<LedgerStatus | null>(null)
  const [statusError, setStatusError] = useState('')
  const [busy, setBusy] = useState<string>('')
  const [toasts, setToasts] = useState<Toast[]>([])

  const [digestLSN, setDigestLSN] = useState('')
  const [digest, setDigest] = useState<LedgerDigest | null>(null)

  const [fromLSN, setFromLSN] = useState('')
  const [toLSN, setToLSN] = useState('')
  const [consistency, setConsistency] = useState<ConsistencyProof | null>(null)

  const [rowKeys, setRowKeys] = useState<RowKeyDraft[]>([{ domain: '', table: '', kind: 'row', pk: '', groupColumns: '' }])
  const [rowProofLSN, setRowProofLSN] = useState('')
  const [rowProof, setRowProof] = useState<RowProof | null>(null)

  const [attestSQL, setAttestSQL] = useState('')
  const [attestDomain, setAttestDomain] = useState('')
  const [attestLSN, setAttestLSN] = useState('')
  const [attestation, setAttestation] = useState<QueryAttestation | null>(null)

  const pushToast = useCallback((message: string, kind: Toast['kind']) => {
    const id = ++toastSeq
    setToasts((current) => [...current, { id, message, kind }])
    setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), 5000)
  }, [])

  const run = useCallback(async (key: string, action: () => Promise<string>) => {
    setBusy(key)
    try {
      pushToast(await action(), 'success')
    } catch (error) {
      pushToast(error instanceof Error ? error.message : String(error), 'error')
    } finally {
      setBusy('')
    }
  }, [pushToast])

  const loadStatus = useCallback(() => {
    let active = true
    void api<LedgerStatus>('/api/ledger/status', 'POST', {}).then((resp) => {
      if (!active) return
      setStatus(resp)
      setStatusError('')
    }).catch((error) => {
      if (!active) return
      setStatus(null)
      setStatusError(error instanceof Error ? error.message : String(error))
    })
    return () => { active = false }
  }, [])

  useEffect(() => loadStatus(), [loadStatus])

  const signed = Boolean(status?.signed)
  const provable = Boolean(status?.provable)
  const rowsProvable = Boolean(status?.rows_provable)
  const latest = status?.digest

  const copyJSON = (label: string, value: unknown) => {
    void navigator.clipboard.writeText(JSON.stringify(value, null, 2))
    pushToast(`${label} copied as JSON.`, 'success')
  }

  const saveJSON = (name: string, value: unknown) => {
    void run(`save-${name}`, async () => {
      await downloadFile(JSON.stringify(value, null, 2), name, 'application/json')
      return `${name} saved.`
    })
  }

  return (
    <div className="sec-page">
      {/* ── Header ──────────────────────────────────────── */}
      <div className="sec-header">
        <div className="sec-header-text">
          <h2 className="sec-title">Verifiable History</h2>
          <p className="sec-subtitle">
            The node&rsquo;s tamper-evident WAL ledger, the proofs built on it, and signed query results.
            Nothing on this page reaches the WAL.
          </p>
        </div>
        <button className="sec-action-chip" onClick={() => { loadStatus() }}>
          <IconRefresh /> <span>Refresh</span>
        </button>
      </div>

      {/* ── What this node can prove ────────────────────── */}
      <div className="sec-kpi-row">
        <LedgerKPI
          icon={<IconChain />}
          label="Ledger mode"
          value={MODE_LABEL[status?.mode ?? 'off'] ?? '—'}
          color={signed ? 'var(--text-safe)' : 'var(--text-warning)'}
          delay={0}
        />
        <LedgerKPI
          icon={<IconShieldCheck />}
          label="Digest version"
          value={latest?.version ? `v${latest.version}` : '—'}
          color="var(--accent)"
          delay={60}
        />
        <LedgerKPI
          icon={<IconCheck />}
          label="Consistency proofs"
          value={provable ? 'Available' : 'Unavailable'}
          color={provable ? 'var(--text-safe)' : 'var(--text-warning)'}
          delay={120}
        />
        <LedgerKPI
          icon={<IconKey />}
          label="Row proofs"
          value={rowsProvable ? 'Available' : 'Unavailable'}
          color={rowsProvable ? 'var(--text-safe)' : 'var(--text-warning)'}
          delay={180}
        />
      </div>

      {statusError !== '' && (
        <div className="ledger-note warn">
          <IconAlertTriangle />
          <span>
            The ledger could not be read: {statusError}. Reading digests needs the ADMIN privilege.
          </span>
        </div>
      )}
      {status?.explanation && (
        <div className="ledger-note">
          <IconAlertTriangle />
          <span>{status.explanation}</span>
        </div>
      )}

      {/* ── Latest digest ───────────────────────────────── */}
      <div className="glass-section" style={{ animationDelay: '60ms' }}>
        <div className="glass-section-header">
          <span className="glass-section-title">Ledger Digest</span>
          {latest && <span className="sec-catalog-count">covers {digestCoverage(latest)}</span>}
        </div>
        <div className="glass-section-body">
          {latest ? (
            <div className="ledger-facts">
              <LedgerFact label="LSN" value={String(latest.lsn ?? 0)} />
              <LedgerFact label="Chain" value={shortHash(latest.chain)} full={latest.chain} />
              <LedgerFact label="Log tree root" value={shortHash(latest.tree_root)} full={latest.tree_root} />
              <LedgerFact label="State tree root" value={shortHash(latest.state_root)} full={latest.state_root} />
              <LedgerFact label="Term" value={String(latest.term ?? 0)} />
              <LedgerFact label="Node" value={latest.node_id || '—'} />
              <LedgerFact label="Key" value={latest.key_id || '—'} />
              <LedgerFact label="Signed at" value={latest.signed_at || '—'} />
            </div>
          ) : (
            <span className="text-muted">This node holds no ledger digest.</span>
          )}

          <div className="sec-actions-strip" style={{ marginTop: 12 }}>
            <label className="sec-field ledger-field">
              <span className="sec-field-label">Digest covering LSN (blank: the latest)</span>
              <input className="sec-input" value={digestLSN} placeholder="e.g. 128" onChange={(e) => setDigestLSN(e.target.value)} />
            </label>
            <ActionChip
              icon={<IconPlay />}
              label="Fetch digest"
              loading={busy === 'digest'}
              disabled={busy !== ''}
              onClick={() => void run('digest', async () => {
                const resp = await api<LedgerDigest>('/api/ledger/digest', 'POST', { lsn: Number(digestLSN || 0) })
                setDigest(resp)
                return `Digest at LSN ${resp.lsn ?? 0} fetched.`
              })}
            />
          </div>

          {digest && <ProofResult name="digest.json" label="Digest" value={digest} onCopy={copyJSON} onSave={saveJSON} />}
        </div>
      </div>

      {/* ── Consistency proof ───────────────────────────── */}
      <div className="glass-section" style={{ animationDelay: '120ms' }}>
        <div className="glass-section-header">
          <span className="glass-section-title">Consistency Proof</span>
          <span className="sec-catalog-count">the later log extends the earlier one</span>
        </div>
        <div className="glass-section-body">
          <p className="ledger-help">
            Proves that the log signed at the later LSN extends the one signed at the earlier LSN, without
            revealing any record. Both LSNs must be ones a digest was signed at. Verify the result with
            <code> asqlctl -command verify-consistency</code>, which needs only the trusted keys.
          </p>
          <div className="sec-actions-strip">
            <label className="sec-field ledger-field">
              <span className="sec-field-label">From LSN</span>
              <input className="sec-input" value={fromLSN} placeholder="signed digest LSN" onChange={(e) => setFromLSN(e.target.value)} />
            </label>
            <label className="sec-field ledger-field">
              <span className="sec-field-label">To LSN (blank: the latest)</span>
              <input className="sec-input" value={toLSN} onChange={(e) => setToLSN(e.target.value)} />
            </label>
            <ActionChip
              icon={<IconShieldCheck />}
              label="Prove"
              loading={busy === 'consistency'}
              disabled={busy !== '' || !provable || fromLSN.trim() === ''}
              onClick={() => void run('consistency', async () => {
                const resp = await api<ConsistencyProof>('/api/ledger/prove-consistency', 'POST', {
                  from_lsn: Number(fromLSN || 0),
                  to_lsn: Number(toLSN || 0),
                })
                setConsistency(resp)
                return 'Consistency proof built.'
              })}
            />
          </div>
          {consistency && (
            <ProofResult name="consistency-proof.json" label="Consistency proof" value={consistency} onCopy={copyJSON} onSave={saveJSON} />
          )}
        </div>
      </div>

      {/* ── Row proof ───────────────────────────────────── */}
      <div className="glass-section" style={{ animationDelay: '180ms' }}>
        <div className="glass-section-header">
          <span className="glass-section-title">Row Proof</span>
          <span className="sec-catalog-count">membership in a signed state</span>
        </div>
        <div className="glass-section-body">
          <p className="ledger-help">
            Proves in one batch that the rows named here were in the state a signed digest covers, revealing
            nothing about other rows. The tables must be <code>VERIFIABLE</code>, and each row is cited by its
            primary key values in key order.
          </p>
          <p className="ledger-help">
            A <strong>group</strong> entry asks for a whole verifiable group instead: the node adds every row of
            that group and the group leaf, so the proof shows <em>all</em> of them — and an empty group for a row
            that has none, which proves exactly that. Outside a group, a proof cannot show that a row is
            <em> absent</em>. Verify with <code>asqlctl -command verify-rows</code>, which rebuilds each group
            from the proof and fails if a row was left out.
          </p>
          <div className="ledger-key-rows">
            {rowKeys.map((key, index) => {
              const patch = (changes: Partial<RowKeyDraft>) =>
                setRowKeys((rows) => rows.map((row, i) => (i === index ? { ...row, ...changes } : row)))
              return (
                <div className="ledger-key-row" key={`row-key-${index}`}>
                  <label className="sec-field ledger-field ledger-field-narrow">
                    <span className="sec-field-label">Asks for</span>
                    <select className="sec-input" value={key.kind} onChange={(e) => patch({ kind: e.target.value as RowKeyDraft['kind'] })}>
                      <option value="row">One row</option>
                      <option value="group">A whole group</option>
                    </select>
                  </label>
                  <label className="sec-field ledger-field">
                    <span className="sec-field-label">Domain (optional)</span>
                    <input className="sec-input" value={key.domain} onChange={(e) => patch({ domain: e.target.value })} />
                  </label>
                  <label className="sec-field ledger-field">
                    <span className="sec-field-label">Table</span>
                    <input className="sec-input" value={key.table} onChange={(e) => patch({ table: e.target.value })} />
                  </label>
                  <label className="sec-field ledger-field">
                    <span className="sec-field-label">
                      {key.kind === 'group' ? 'Group key values' : 'Primary key values, in key order'}
                    </span>
                    <input
                      className="sec-input"
                      value={key.pk}
                      placeholder={key.kind === 'group' ? 'e.g. 0192abc' : 'e.g. 7, 2'}
                      onChange={(e) => patch({ pk: e.target.value })}
                    />
                  </label>
                  {key.kind === 'group' && (
                    <label className="sec-field ledger-field">
                      <span className="sec-field-label">Group columns (only with several)</span>
                      <input
                        className="sec-input"
                        value={key.groupColumns}
                        placeholder="e.g. child_id"
                        onChange={(e) => patch({ groupColumns: e.target.value })}
                      />
                    </label>
                  )}
                  <button
                    className="icon-btn danger"
                    title="Remove this entry"
                    disabled={rowKeys.length <= 1}
                    onClick={() => setRowKeys((rows) => rows.filter((_, i) => i !== index))}
                  >×</button>
                </div>
              )
            })}
          </div>
          <div className="sec-actions-strip">
            <ActionChip
              icon={<IconPlay />}
              label="Add row"
              disabled={busy !== ''}
              onClick={() => setRowKeys((rows) => [...rows, { domain: '', table: '', kind: 'row', pk: '', groupColumns: '' }])}
            />
            <label className="sec-field ledger-field">
              <span className="sec-field-label">At LSN (blank: the latest digest)</span>
              <input className="sec-input" value={rowProofLSN} onChange={(e) => setRowProofLSN(e.target.value)} />
            </label>
            <ActionChip
              icon={<IconShieldCheck />}
              label="Prove rows"
              loading={busy === 'rows'}
              disabled={busy !== '' || !rowsProvable || rowKeys.every((key) => key.table.trim() === '')}
              onClick={() => void run('rows', async () => {
                const split = (value: string) => value.split(',').map((part) => part.trim()).filter(Boolean)
                const keys = rowKeys
                  .filter((key) => key.table.trim() !== '')
                  .map((key) => (key.kind === 'group'
                    ? {
                      domain: key.domain.trim(),
                      table: key.table.trim(),
                      group: split(key.pk),
                      group_columns: split(key.groupColumns),
                    }
                    : {
                      domain: key.domain.trim(),
                      table: key.table.trim(),
                      pk: split(key.pk),
                    }))
                const resp = await api<RowProof>('/api/ledger/prove-rows', 'POST', { keys, lsn: Number(rowProofLSN || 0) })
                setRowProof(resp)
                const groupCount = keys.filter((key) => 'group' in key).length
                return groupCount > 0
                  ? `Proof built for ${keys.length - groupCount} row(s) and ${groupCount} complete group(s).`
                  : `Proof built for ${keys.length} row${keys.length !== 1 ? 's' : ''}.`
              })}
            />
          </div>
          {rowProof && <ProofResult name="row-proof.json" label="Row proof" value={rowProof} onCopy={copyJSON} onSave={saveJSON} />}
        </div>
      </div>

      {/* ── Attested query ──────────────────────────────── */}
      <div className="glass-section" style={{ animationDelay: '240ms' }}>
        <div className="glass-section-header">
          <span className="glass-section-title">Attested Query</span>
          <span className="sec-catalog-count">a signed result</span>
        </div>
        <div className="glass-section-body">
          <p className="ledger-help">
            Runs a SELECT at a committed LSN and signs its rows together with the SQL and the chain value at
            that LSN. <code>asqlctl -command verify-result</code> checks the signature, the rows and the chain,
            and can re-execute the query separately.
          </p>
          <label className="sec-field">
            <span className="sec-field-label">SELECT to attest</span>
            <textarea
              className="sec-input mono"
              rows={3}
              value={attestSQL}
              placeholder="SELECT id, amount FROM ledger ORDER BY id"
              onChange={(e) => setAttestSQL(e.target.value)}
            />
          </label>
          <div className="sec-actions-strip">
            <label className="sec-field ledger-field">
              <span className="sec-field-label">Domain</span>
              <input className="sec-input" value={attestDomain} onChange={(e) => setAttestDomain(e.target.value)} />
            </label>
            <label className="sec-field ledger-field">
              <span className="sec-field-label">At LSN (blank: the latest commit)</span>
              <input className="sec-input" value={attestLSN} onChange={(e) => setAttestLSN(e.target.value)} />
            </label>
            <ActionChip
              icon={<IconShieldCheck />}
              label="Attest"
              loading={busy === 'attest'}
              disabled={busy !== '' || !signed || attestSQL.trim() === ''}
              onClick={() => void run('attest', async () => {
                const resp = await api<QueryAttestation>('/api/ledger/attest-query', 'POST', {
                  sql: attestSQL.trim(),
                  domains: attestDomain.trim() === '' ? [] : [attestDomain.trim()],
                  lsn: Number(attestLSN || 0),
                })
                setAttestation(resp)
                return 'Result attested.'
              })}
            />
          </div>
          {attestation && (
            <ProofResult name="attestation.json" label="Attestation" value={attestation} onCopy={copyJSON} onSave={saveJSON} />
          )}
        </div>
      </div>

      {/* ── Toast rail ──────────────────────────────────── */}
      {toasts.length > 0 && (
        <div className="sec-toast-rail">
          {toasts.map((toast) => (
            <div key={toast.id} className={`sec-toast sec-toast-${toast.kind}`}>
              {toast.kind === 'success' ? <IconCheck /> : <IconAlertTriangle />}
              <span>{toast.message}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/* ── Sub-components ───────────────────────────────────────── */

function LedgerKPI({
  icon,
  label,
  value,
  color,
  delay = 0,
}: {
  icon: ReactNode
  label: string
  value: string
  color: string
  delay?: number
}) {
  return (
    <div className="kpi-card" style={{ '--kpi-accent': color, animationDelay: `${delay}ms` } as React.CSSProperties}>
      <div className="kpi-card-glow" />
      <div className="kpi-icon-wrap" style={{ color }}>{icon}</div>
      <div className="kpi-content">
        <span className="kpi-label">{label}</span>
        <div className="kpi-value kpi-value-text">{value}</div>
      </div>
    </div>
  )
}

function LedgerFact({ label, value, full }: { label: string; value: string; full?: string }) {
  return (
    <div className="ledger-fact" title={full || undefined}>
      <span className="ledger-fact-label">{label}</span>
      <span className="ledger-fact-value mono">{value}</span>
    </div>
  )
}

function ProofResult({
  name,
  label,
  value,
  onCopy,
  onSave,
}: {
  name: string
  label: string
  value: unknown
  onCopy: (label: string, value: unknown) => void
  onSave: (name: string, value: unknown) => void
}) {
  return (
    <div className="ledger-result">
      <div className="ledger-result-bar">
        <span className="ledger-result-label">{label}</span>
        <button className="sec-action-chip" onClick={() => onCopy(label, value)}><IconCopy /> <span>Copy</span></button>
        <button className="sec-action-chip" onClick={() => onSave(name, value)}><IconDownload /> <span>Save</span></button>
      </div>
      <div className="ledger-result-body">
        <JsonTreeView data={value} />
      </div>
    </div>
  )
}

function ActionChip({
  icon,
  label,
  loading,
  onClick,
  disabled,
}: {
  icon: ReactNode
  label: string
  loading?: boolean
  onClick: () => void
  disabled: boolean
}) {
  return (
    <button className={`sec-action-chip ${loading ? 'loading' : ''}`} onClick={onClick} disabled={disabled}>
      {icon}
      <span>{label}</span>
    </button>
  )
}
