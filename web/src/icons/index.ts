const paths: Record<string, string> = {
  activity: '<path d="M3 12h4l2.2-7 5.6 14L17 12h4"/>',
  alert: '<path d="M12 4 3 20h18L12 4Z"/><path d="M12 9v5m0 3h.01"/>',
  clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7v5l3 2"/>',
  layers: '<path d="m12 4 8 4-8 4-8-4 8-4Z"/><path d="m4 12 8 4 8-4M4 16l8 4 8-4"/>',
  link: '<path d="m9 15-2 2a3 3 0 1 1-4-4l4-4a3 3 0 0 1 4 0m0-3 2-2a3 3 0 0 1 4 4l-4 4a3 3 0 0 1-4 0"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>',
  server: '<rect x="4" y="4" width="16" height="6" rx="1"/><rect x="4" y="14" width="16" height="6" rx="1"/><path d="M8 7h.01M8 17h.01"/>',
  spark: '<path d="m12 2 1.4 7.6L21 11l-7.6 1.4L12 20l-1.4-7.6L3 11l7.6-1.4L12 2Z"/>',
};

export function icon(name: string, label?: string): string {
  const title = label ? `<title>${label}</title>` : "";
  return `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="${label ? "false" : "true"}"${label ? ` role="img" aria-label="${label}"` : ""}>${title}${paths[name] ?? paths.spark}</svg>`;
}
