import type { BaseTransport, TransportItem } from '@grafana/faro-web-sdk';

export interface TelemetryConfig {
  url?: string;
  app: { name: string; version: string; environment: string };
  routes?: string[];
  assets?: string[];
  operations?: string[];
  local?: boolean;
  transports?: BaseTransport[];
}

export function initializeTelemetry(config: TelemetryConfig): {
  captureError(error: unknown, operation?: string): void;
  dispose(): void;
};

export function privacyFilter(config: Pick<TelemetryConfig, 'app' | 'routes' | 'assets' | 'operations'> & { origin: string }): (item: TransportItem) => TransportItem | null;
