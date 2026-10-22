import { useEffect, useState } from 'react'
import type { Dispatch, ReactNode, SetStateAction } from 'react'
import {
  clone,
  dropColumnFromPrimaryKey,
  renameColumnInPrimaryKey,
  setTablePrimaryKey,
  tablePrimaryKey,
  type SchemaColumn,
  type SchemaModel,
  type SchemaTable,
} from '../schema'
import { IconChevronRight, IconKey, IconLink, IconLock, IconNullable, IconPlus, IconShieldCheck, IconTrash, IconUnique } from './Icons'

type Props = {
  model: SchemaModel
  setModel: Dispatch<SetStateAction<SchemaModel>>
  selectedTable: number
  selectedColumn: number
  setSelectedColumn: Dispatch<SetStateAction<number>>
  activeTable: SchemaTable | null
  activeColumn: SchemaColumn | null
  updateTable: (updater: (table: SchemaTable) => SchemaTable) => void
  updateColumn: (updater: (column: SchemaColumn) => SchemaColumn) => void
}

const COLUMN_TYPES = ['INT', 'TEXT', 'JSON', 'BOOL', 'FLOAT', 'TIMESTAMP'] as const

function inferDefaultMode(value: string): string {
  if (!value) return 'none'
  const upper = value.toUpperCase()
  if (upper === 'NULL') return 'null'
  if (upper === 'AUTOINCREMENT') return 'autoincrement'
  if (upper === 'UUID_V7') return 'uuid_v7'
  if (upper === 'TX_TIMESTAMP') return 'tx_timestamp'
  return 'value'
}

function SectionPanel({
  label,
  badge,
  open,
  onToggle,
  actions,
  children,
  indent,
}: {
  label: string
  badge?: string | null
  open: boolean
  onToggle: () => void
  actions?: ReactNode
  children: ReactNode
  indent?: boolean
}) {
  return (
    <div className={`ce-section${open ? ' open' : ''}${indent ? ' ce-indent' : ''}`}>
      <button className="ce-section-hd" onClick={onToggle}>
        <span className={`ce-chevron${open ? ' open' : ''}`}><IconChevronRight /></span>
        <span className="ce-section-label">{label}</span>
        {badge != null && badge !== '' && <span className="ce-section-badge">{badge}</span>}
        {actions && (
          <span className="ce-section-acts" onClick={(e) => e.stopPropagation()}>
            {actions}
          </span>
        )}
      </button>
      {open && <div className="ce-section-body">{children}</div>}
    </div>
  )
}

export function ColumnEditor({
  model,
  setModel,
  selectedTable: _selectedTable,
  selectedColumn,
  setSelectedColumn,
  activeTable,
  activeColumn,
  updateTable,
  updateColumn,
}: Props) {
  const [tableOpen, setTableOpen] = useState(true)
  const [colsOpen, setColsOpen] = useState(true)
  const [propsOpen, setPropsOpen] = useState(true)
  const [fkOpen, setFkOpen] = useState(false)
  const [vfkOpen, setVfkOpen] = useState(false)

  // Auto-open properties panel when a different column is selected
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (activeColumn) setPropsOpen(true)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedColumn])

  // Auto-open FK panel if the selected column already has a reference set
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (activeColumn?.references?.table) setFkOpen(true)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedColumn])

  if (!activeTable) {
    return (
      <div className="column-editor">
        <div className="panel-empty">
          <span className="text-muted">Select a table to edit columns</span>
        </div>
      </div>
    )
  }

  const addColumn = () => {
    updateTable((table) => ({
      ...table,
      columns: [
        ...table.columns,
        { name: `col_${table.columns.length + 1}`, type: 'TEXT', nullable: true, primary_key: false, unique: false, default_value: '' },
      ],
    }))
    setSelectedColumn(activeTable.columns.length)
  }

  const removeColumn = () => {
    if (activeTable.columns.length <= 1) return
    const removed = activeTable.columns[selectedColumn]?.name ?? ''
    updateTable((table) => {
      const next = dropColumnFromPrimaryKey(clone(table), removed)
      next.columns.splice(selectedColumn, 1)
      return next
    })
    setSelectedColumn(Math.max(0, selectedColumn - 1))
  }

  const vfks = activeTable.versioned_foreign_keys || []
  const primaryKey = tablePrimaryKey(activeTable)
  const keyPosition = (name: string) => primaryKey.indexOf(name)

  // The key is edited as one list: a single column lands on the column itself,
  // several become a table-level PRIMARY KEY (a, b). Order is part of the key.
  const toggleKeyColumn = (name: string) => {
    updateTable((table) => {
      const current = tablePrimaryKey(table)
      const next = current.includes(name) ? current.filter((column) => column !== name) : [...current, name]
      return setTablePrimaryKey(table, next)
    })
  }

  const moveKeyColumn = (name: string, delta: number) => {
    updateTable((table) => {
      const current = tablePrimaryKey(table)
      const from = current.indexOf(name)
      const to = from + delta
      if (from < 0 || to < 0 || to >= current.length) return table
      const next = [...current]
      next.splice(to, 0, ...next.splice(from, 1))
      return setTablePrimaryKey(table, next)
    })
  }

  const renameActiveColumn = (name: string) => {
    updateTable((table) => {
      const previous = table.columns[selectedColumn]?.name ?? ''
      const next = renameColumnInPrimaryKey(clone(table), previous, name)
      next.columns[selectedColumn] = { ...next.columns[selectedColumn], name }
      return next
    })
  }

  return (
    <div className="column-editor">

      {/* ── Table identity ──────────────────────────────────── */}
      <SectionPanel
        label={activeTable.name || 'untitled'}
        badge={model.domain || null}
        open={tableOpen}
        onToggle={() => setTableOpen((v) => !v)}
      >
        <div className="editor-field">
          <label className="editor-field-label">Table Name</label>
          <input
            className="editor-input"
            value={activeTable.name}
            onChange={(e) => updateTable((t) => ({ ...t, name: e.target.value }))}
          />
        </div>
        <div className="editor-field">
          <label className="editor-field-label">Domain</label>
          <input
            className="editor-input"
            value={model.domain}
            onChange={(e) => setModel((c) => ({ ...c, domain: e.target.value }))}
          />
        </div>

        <div className="editor-field">
          <label className="editor-field-label">
            Primary Key{primaryKey.length > 1 ? ` (${primaryKey.join(', ')})` : ''}
          </label>
          <div className="ce-key-list">
            {activeTable.columns.map((col) => {
              const position = keyPosition(col.name)
              const inKey = position >= 0
              return (
                <div key={`key-${col.name}`} className={`ce-key-row${inKey ? ' on' : ''}`}>
                  <label className="ce-key-toggle">
                    <input type="checkbox" checked={inKey} onChange={() => toggleKeyColumn(col.name)} />
                    <IconKey />
                    <span className="mono">{col.name || '—'}</span>
                  </label>
                  {inKey && primaryKey.length > 1 && (
                    <span className="ce-key-order">
                      <span className="ce-key-position">{position + 1}</span>
                      <button
                        className="icon-btn"
                        title="Earlier in the key"
                        disabled={position === 0}
                        onClick={() => moveKeyColumn(col.name, -1)}
                      >↑</button>
                      <button
                        className="icon-btn"
                        title="Later in the key"
                        disabled={position === primaryKey.length - 1}
                        onClick={() => moveKeyColumn(col.name, 1)}
                      >↓</button>
                    </span>
                  )}
                </div>
              )
            })}
          </div>
          {primaryKey.length > 1 && (
            <span className="editor-hint">
              Column order is part of the key. Key columns are NOT NULL and the key tuple must be unique.
            </span>
          )}
          {primaryKey.length === 0 && (
            <span className="editor-hint">
              No primary key. A table needs one to be VERIFIABLE or to have its rows proven.
            </span>
          )}
        </div>

        <div className="constraint-grid">
          <label className={`constraint-toggle ${activeTable.append_only ? 'on' : ''}`}>
            <input
              type="checkbox"
              checked={Boolean(activeTable.append_only)}
              onChange={(e) => updateTable((t) => ({ ...t, append_only: e.target.checked }))}
            />
            <IconLock /> <span>Append Only</span>
          </label>
          <label className={`constraint-toggle ${activeTable.verifiable ? 'on' : ''}`}>
            <input
              type="checkbox"
              checked={Boolean(activeTable.verifiable)}
              onChange={(e) => updateTable((t) => ({ ...t, verifiable: e.target.checked }))}
            />
            <IconShieldCheck /> <span>Verifiable</span>
          </label>
        </div>
        {(activeTable.verifiable_groups ?? []).length > 0 && (
          <div className="editor-field">
            <label className="editor-field-label">Verifiable Groups</label>
            <div className="ce-group-list">
              {(activeTable.verifiable_groups ?? []).map((group, index) => (
                <div className="ce-group-row" key={`group-${index}`}>
                  <IconShieldCheck />
                  <span className="mono">
                    ({group.columns.join(', ')}) → {group.references} ({group.referenced_columns.join(', ')})
                  </span>
                </div>
              ))}
            </div>
            <span className="editor-hint">
              Each group makes the rows sharing a key value provable as a complete set. Declared with its own
              ALTER TABLE and never removed, so it is shown here, not edited.
            </span>
          </div>
        )}

        {(activeTable.append_only || activeTable.verifiable) && (
          <span className="editor-hint warn">
            {activeTable.append_only && 'APPEND ONLY stops UPDATE and DELETE on this table. '}
            {activeTable.verifiable && 'VERIFIABLE puts the table\u2019s live rows in the state tree so they can be proven against a signed digest. '}
            Neither can be undone once applied.
          </span>
        )}
      </SectionPanel>

      {/* ── Columns list ────────────────────────────────────── */}
      <SectionPanel
        label="Columns"
        badge={String(activeTable.columns.length)}
        open={colsOpen}
        onToggle={() => setColsOpen((v) => !v)}
        actions={
          <>
            <button className="icon-btn" onClick={addColumn} title="Add column"><IconPlus /></button>
            <button className="icon-btn danger" onClick={removeColumn} title="Remove column" disabled={activeTable.columns.length <= 1}><IconTrash /></button>
          </>
        }
      >
        <div className="column-list">
          {activeTable.columns.map((col, i) => (
            <button
              key={`${col.name}-${i}`}
              className={`column-list-item ${i === selectedColumn ? 'active' : ''}`}
              onClick={() => setSelectedColumn(i)}
            >
              <span className="col-indicator">
                {keyPosition(col.name) >= 0 ? <IconKey /> : col.references ? <IconLink /> : <span className="col-dot" />}
              </span>
              <span className="col-list-name">{col.name || '—'}</span>
              <span className="col-list-type">{col.type}</span>
            </button>
          ))}
        </div>
      </SectionPanel>

      {/* ── Column properties ───────────────────────────────── */}
      {activeColumn && (
        <SectionPanel
          label={activeColumn.name || 'column'}
          badge={activeColumn.type}
          open={propsOpen}
          onToggle={() => setPropsOpen((v) => !v)}
        >
          <div className="editor-row">
            <div className="editor-field">
              <label className="editor-field-label">Name</label>
              <input
                className="editor-input mono"
                value={activeColumn.name}
                onChange={(e) => renameActiveColumn(e.target.value)}
              />
            </div>
            <div className="editor-field">
              <label className="editor-field-label">Type</label>
              <select
                className="editor-input"
                value={activeColumn.type}
                onChange={(e) => updateColumn((c) => ({ ...c, type: e.target.value }))}
              >
                {COLUMN_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
              </select>
            </div>
          </div>

          <div className="editor-field">
            <label className="editor-field-label">Default Value</label>
            <div className="default-mode-row">
              <select
                className="editor-input default-mode-select"
                value={inferDefaultMode(activeColumn.default_value || '')}
                onChange={(e) => {
                  const mode = e.target.value
                  if (mode === 'none') updateColumn((c) => ({ ...c, default_value: '' }))
                  else if (mode === 'null') updateColumn((c) => ({ ...c, default_value: 'NULL' }))
                  else if (mode === 'autoincrement') updateColumn((c) => ({ ...c, default_value: 'AUTOINCREMENT' }))
                  else if (mode === 'uuid_v7') updateColumn((c) => ({ ...c, default_value: 'UUID_V7' }))
                  else if (mode === 'tx_timestamp') updateColumn((c) => ({ ...c, default_value: 'TX_TIMESTAMP' }))
                  else if (mode === 'value') updateColumn((c) => ({ ...c, default_value: c.default_value && !['NULL', 'AUTOINCREMENT', 'UUID_V7', 'TX_TIMESTAMP'].includes(c.default_value.toUpperCase()) ? c.default_value : '0' }))
                }}
              >
                <option value="none">None</option>
                <option value="value">Value</option>
                <option value="null">NULL</option>
                <option value="autoincrement">AUTOINCREMENT</option>
                <option value="uuid_v7">UUID_V7</option>
                <option value="tx_timestamp">TX_TIMESTAMP</option>
              </select>
              {inferDefaultMode(activeColumn.default_value || '') === 'value' && (
                <input
                  className="editor-input mono default-value-input"
                  value={activeColumn.default_value || ''}
                  placeholder="e.g. 0, 'hello', TRUE"
                  onChange={(e) => updateColumn((c) => ({ ...c, default_value: e.target.value }))}
                />
              )}
            </div>
          </div>

          <div className="constraint-grid">
            <label className={`constraint-toggle ${keyPosition(activeColumn.name) >= 0 ? 'on' : ''}`}>
              <input
                type="checkbox"
                checked={keyPosition(activeColumn.name) >= 0}
                onChange={() => toggleKeyColumn(activeColumn.name)}
              />
              <IconKey />
              <span>
                Primary Key
                {keyPosition(activeColumn.name) >= 0 && primaryKey.length > 1 ? ` ${keyPosition(activeColumn.name) + 1}/${primaryKey.length}` : ''}
              </span>
            </label>
            <label className={`constraint-toggle ${activeColumn.unique ? 'on' : ''}`}>
              <input type="checkbox" checked={activeColumn.unique} onChange={(e) => updateColumn((c) => ({ ...c, unique: e.target.checked }))} />
              <IconUnique /> <span>Unique</span>
            </label>
            <label className={`constraint-toggle ${activeColumn.nullable ? 'on' : ''}`}>
              <input
                type="checkbox"
                checked={activeColumn.nullable}
                disabled={keyPosition(activeColumn.name) >= 0}
                title={keyPosition(activeColumn.name) >= 0 ? 'A primary key column cannot be NULL' : undefined}
                onChange={(e) => updateColumn((c) => ({ ...c, nullable: e.target.checked }))}
              />
              <IconNullable /> <span>Nullable</span>
            </label>
          </div>

          {/* FK Reference — nested collapsible */}
          <SectionPanel
            label="FK Reference"
            badge={activeColumn.references?.table ? `→ ${activeColumn.references.table}` : null}
            open={fkOpen}
            onToggle={() => setFkOpen((v) => !v)}
            indent
          >
            <div className="fk-row">
              <div className="editor-field">
                <label className="editor-field-label">Table</label>
                <input
                  className="editor-input mono"
                  placeholder="table"
                  value={activeColumn.references?.table || ''}
                  onChange={(e) => updateColumn((c) => ({ ...c, references: { table: e.target.value, column: c.references?.column || '' } }))}
                />
              </div>
              <span className="fk-dot">.</span>
              <div className="editor-field">
                <label className="editor-field-label">Column</label>
                <input
                  className="editor-input mono"
                  placeholder="column"
                  value={activeColumn.references?.column || ''}
                  onChange={(e) => updateColumn((c) => ({ ...c, references: { table: c.references?.table || '', column: e.target.value } }))}
                />
              </div>
            </div>
          </SectionPanel>
        </SectionPanel>
      )}

      {/* ── Versioned FKs ───────────────────────────────────── */}
      <SectionPanel
        label="Versioned FKs"
        badge={vfks.length > 0 ? String(vfks.length) : null}
        open={vfkOpen}
        onToggle={() => setVfkOpen((v) => !v)}
        actions={
          <button
            className="icon-btn"
            onClick={() =>
              updateTable((t) => ({
                ...t,
                versioned_foreign_keys: [
                  ...(t.versioned_foreign_keys || []),
                  { column: '', lsn_column: '', references_domain: '', references_table: '', references_column: '' },
                ],
              }))
            }
            title="Add versioned FK"
          >
            <IconPlus />
          </button>
        }
      >
        {vfks.length === 0 ? (
          <div className="panel-empty" style={{ padding: '8px 0' }}>
            <span className="text-muted">No versioned FKs defined</span>
          </div>
        ) : (
          vfks.map((vfk, i) => (
            <div key={i} className="vfk-entry">
              <div className="editor-row">
                <div className="editor-field">
                  <label className="editor-field-label">Column</label>
                  <input
                    className="editor-input mono"
                    placeholder="fk_column"
                    value={vfk.column}
                    onChange={(e) =>
                      updateTable((t) => {
                        const next = clone(t)
                        const fks = next.versioned_foreign_keys || []
                        fks[i] = { ...fks[i], column: e.target.value }
                        next.versioned_foreign_keys = fks
                        return next
                      })
                    }
                  />
                </div>
                <div className="editor-field">
                  <label className="editor-field-label">LSN Column</label>
                  <input
                    className="editor-input mono"
                    placeholder="lsn_column"
                    value={vfk.lsn_column}
                    onChange={(e) =>
                      updateTable((t) => {
                        const next = clone(t)
                        const fks = next.versioned_foreign_keys || []
                        fks[i] = { ...fks[i], lsn_column: e.target.value }
                        next.versioned_foreign_keys = fks
                        return next
                      })
                    }
                  />
                </div>
              </div>

              <div className="fk-row">
                <div className="editor-field" style={{ flex: 1 }}>
                  <label className="editor-field-label">Domain</label>
                  <input
                    className="editor-input mono"
                    placeholder="domain"
                    value={vfk.references_domain}
                    onChange={(e) =>
                      updateTable((t) => {
                        const next = clone(t)
                        const fks = next.versioned_foreign_keys || []
                        fks[i] = { ...fks[i], references_domain: e.target.value }
                        next.versioned_foreign_keys = fks
                        return next
                      })
                    }
                  />
                </div>
                <span className="fk-dot">.</span>
                <div className="editor-field" style={{ flex: 1 }}>
                  <label className="editor-field-label">Table</label>
                  <input
                    className="editor-input mono"
                    placeholder="table"
                    value={vfk.references_table}
                    onChange={(e) =>
                      updateTable((t) => {
                        const next = clone(t)
                        const fks = next.versioned_foreign_keys || []
                        fks[i] = { ...fks[i], references_table: e.target.value }
                        next.versioned_foreign_keys = fks
                        return next
                      })
                    }
                  />
                </div>
                <span className="fk-dot">(</span>
                <div className="editor-field" style={{ flex: 1 }}>
                  <label className="editor-field-label">Column</label>
                  <input
                    className="editor-input mono"
                    placeholder="column"
                    value={vfk.references_column}
                    onChange={(e) =>
                      updateTable((t) => {
                        const next = clone(t)
                        const fks = next.versioned_foreign_keys || []
                        fks[i] = { ...fks[i], references_column: e.target.value }
                        next.versioned_foreign_keys = fks
                        return next
                      })
                    }
                  />
                </div>
                <span className="fk-dot">)</span>
                <button
                  className="icon-btn danger"
                  title="Remove versioned FK"
                  onClick={() =>
                    updateTable((t) => {
                      const next = clone(t)
                      const fks = next.versioned_foreign_keys || []
                      fks.splice(i, 1)
                      next.versioned_foreign_keys = fks.length > 0 ? fks : undefined
                      return next
                    })
                  }
                >
                  <IconTrash />
                </button>
              </div>
            </div>
          ))
        )}
      </SectionPanel>

    </div>
  )
}

