import Ajv2020 from 'ajv/dist/2020';
import schema from '../../schemas/doctor.schema.json';

export type Check = { id: string; status: 'available' | 'unavailable' | 'unverified' | 'not_configured'; reason: string; version?: string };
export type Report = { schema_version: '0.1'; stage: 'foundation'; host: { os: string; arch: string; logical_cpus: number; memory_total_bytes: number | null; disk_available_bytes: number | null }; checks: Check[] };
const validate = new Ajv2020({ strict: true }).compile<Report>(schema);
export function parseReport(text: string): Report {
  if (new TextEncoder().encode(text).length > 1048576) throw new Error('size');
  const value: unknown = JSON.parse(text);
  if (!validate(value)) throw new Error('schema');
  const ids = value.checks.map(check => check.id);
  if (new Set(ids).size !== ids.length) throw new Error('duplicate');
  return value;
}
