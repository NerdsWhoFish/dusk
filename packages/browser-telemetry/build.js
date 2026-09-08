import { execFileSync } from 'node:child_process';
import { build } from 'esbuild';

export async function buildTelemetry({ entry, outfile, revision = process.env.APP_VERSION }) {
  revision ??= execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error('A Git build revision is required');
  return build({
    entryPoints: [entry], outfile, bundle: true, format: 'iife', platform: 'browser',
    target: 'es2022', minify: true, sourcemap: true,
    define: { __APP_VERSION__: JSON.stringify(revision) },
  });
}
