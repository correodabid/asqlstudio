export type SchemaReference = {
  table: string
  column: string
}

export type SchemaColumn = {
  name: string
  type: string
  nullable: boolean
  // primary_key is set only for a single-column key; the whole key lives on
  // the table, which tablePrimaryKey() reads.
  primary_key: boolean
  unique: boolean
  default_value?: string
  references?: SchemaReference
}

export type SchemaIndex = {
  name: string
  columns: string[]
  method: 'hash' | 'btree'
}

export type VersionedFK = {
  column: string
  lsn_column: string
  references_domain: string
  references_table: string
  references_column: string
}

// A verifiable group makes the rows that share a key value provable as a
// complete set: a proof can show all of them, and that a row has none, without
// a proof of absence. It is declared with its own ALTER TABLE and cannot be
// undone, so the designer shows groups rather than letting them be edited.
export type VerifiableGroup = {
  columns: string[]
  references: string
  referenced_columns: string[]
}

export type SchemaTable = {
  name: string
  columns: SchemaColumn[]
  indexes?: SchemaIndex[]
  versioned_foreign_keys?: VersionedFK[]
  verifiable_groups?: VerifiableGroup[]
  // primary_key holds the key columns in key order. A single-column key may
  // instead be marked on the column; tablePrimaryKey() reads both forms. A
  // composite key can only be expressed here.
  primary_key?: string[]
  // append_only and verifiable cannot be undone once applied.
  append_only?: boolean
  verifiable?: boolean
}

// tablePrimaryKey is the table's key in key order, whichever form it was
// expressed in.
export const tablePrimaryKey = (table: SchemaTable): string[] => {
  const declared = (table.primary_key ?? []).map(column => column.trim()).filter(Boolean)
  if (declared.length > 0) return declared
  return table.columns.filter(column => column.primary_key).map(column => column.name.trim()).filter(Boolean)
}

// isCompositePrimaryKey reports a key the engine can only express as a
// table-level constraint.
export const isCompositePrimaryKey = (table: SchemaTable): boolean => tablePrimaryKey(table).length > 1

// setTablePrimaryKey makes one key the table's, in the given column order, and
// normalizes how it is expressed: a single-column key sits on its column, a
// composite one on the table. Every key column becomes NOT NULL, which the
// engine requires.
export const setTablePrimaryKey = (table: SchemaTable, key: string[]): SchemaTable => {
  const declared = new Set(table.columns.map(column => column.name))
  const unique = [...new Set(key.map(column => column.trim()).filter(column => column && declared.has(column)))]
  const inKey = new Set(unique)
  const next: SchemaTable = {
    ...table,
    columns: table.columns.map(column => ({
      ...column,
      primary_key: unique.length === 1 && inKey.has(column.name),
      nullable: inKey.has(column.name) ? false : column.nullable,
    })),
  }
  if (unique.length > 1) next.primary_key = unique
  else delete next.primary_key
  return next
}

// renameColumnInPrimaryKey keeps the table-level key pointing at a column that
// was renamed; a key naming a column that no longer exists cannot be rendered.
export const renameColumnInPrimaryKey = (table: SchemaTable, from: string, to: string): SchemaTable => {
  if (!table.primary_key || from === to) return table
  return { ...table, primary_key: table.primary_key.map(column => (column === from ? to : column)) }
}

// dropColumnFromPrimaryKey removes a deleted column from the table-level key.
export const dropColumnFromPrimaryKey = (table: SchemaTable, name: string): SchemaTable => {
  if (!table.primary_key) return table
  const remaining = table.primary_key.filter(column => column !== name)
  if (remaining.length === table.primary_key.length) return table
  return setTablePrimaryKey(table, remaining)
}

export type EntityDefinition = {
  name: string
  root_table: string
  tables: string[]
}

export type SchemaModel = {
  domain: string
  tables: SchemaTable[]
  entities?: EntityDefinition[]
}

export type MultiDomainModel = {
  domains: SchemaModel[]
}

export type DiffOperation = {
  type: string
  table: string
  column?: string
  statement?: string
  safe: boolean
  breaking?: boolean
  reason?: string
}

export type DiffResponse = {
  domain: string
  safe: boolean
  operations: DiffOperation[]
  statements: string[]
  warnings?: string[]
}

export type ApplySafeDiffResponse = {
  status: string
  tx_id: string
  domain: string
  diff_safe: boolean
  applied_count: number
  unsafe_count: number
  executed_statements: string[]
  warnings?: string[]
}

export type DDLResponse = {
  ddl: string
  statements: string[]
}

export const DEFAULT_MODEL: SchemaModel = {
  domain: 'default',
  tables: [],
}

export const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value)) as T
