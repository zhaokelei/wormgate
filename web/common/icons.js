/**
 * Line icons, inlined as path data.
 *
 * Why not an icon package: the consoles run under a strict CSP with
 * `img-src 'self' data:` and ship inside a Go binary. A shared `<Icon>` with
 * ~40 inline paths costs a couple of KB and zero network requests, versus a
 * dependency plus a sprite request.
 *
 * All paths target a 24x24 box and are stroked (not filled), so they inherit
 * `currentColor` and stay sharp at the 13-20px sizes used in the UI.
 */
export const icons = {
  // -- navigation / structure
  dashboard: 'M3 13h6V3H3v10Zm0 8h6v-5H3v5Zm8 0h10V11H11v10Zm0-18v5h10V3H11Z',
  tunnel: 'M4 7h16M4 12h10M4 17h7',
  activity: 'M3 12h4l3-8 4 16 3-8h4',
  users: 'M16 20v-1a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v1M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm13 9v-1a4 4 0 0 0-3-3.87M16 3.13A4 4 0 0 1 16 11',
  server: 'M4 4h16v6H4V4Zm0 10h16v6H4v-6Zm4-7h.01M8 17h.01',
  shield: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z',
  list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm7.4-3a7.4 7.4 0 0 0-.1-1.2l2-1.6-2-3.4-2.4 1a7.5 7.5 0 0 0-2-1.2L14.5 3h-4l-.4 2.6c-.7.3-1.4.7-2 1.2l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 0 0 0 2.4l-2 1.6 2 3.4 2.4-1c.6.5 1.3.9 2 1.2l.4 2.6h4l.4-2.6c.7-.3 1.4-.7 2-1.2l2.4 1 2-3.4-2-1.6c.1-.4.1-.8.1-1.2Z',
  terminal: 'm4 17 6-6-6-6M12 19h8',

  // -- status
  check: 'm20 6-11 11-5-5',
  checkCircle: 'M22 11.1V12a10 10 0 1 1-5.9-9.1M22 4 12 14.01l-3-3',
  alertTriangle: 'M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.7 3.86a2 2 0 0 0-3.4 0ZM12 9v4M12 17h.01',
  alertCircle: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Zm0-6v.01M12 8v4',
  info: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Zm0-14v.01M12 12v5',
  x: 'M18 6 6 18M6 6l12 12',
  xCircle: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Zm-3-7 6-6m0 6-6-6',
  bell: 'M18 8a6 6 0 1 0-12 0c0 7-3 8-3 8h18s-3-1-3-8M13.7 21a2 2 0 0 1-3.4 0',
  wifi: 'M5 12.55a11 11 0 0 1 14 0M8.5 16.5a6 6 0 0 1 7 0M2 8.82a15 15 0 0 1 20 0M12 20h.01',
  wifiOff: 'M2 2l20 20M8.5 16.5a6 6 0 0 1 7 0M5 12.55a11 11 0 0 1 5.2-2.9M2 8.82a15 15 0 0 1 4.2-2.6M12 20h.01',

  // -- actions
  plus: 'M12 5v14M5 12h14',
  refresh: 'M21 12a9 9 0 1 1-3-6.7M21 3v6h-6',
  play: 'm6 4 14 8-14 8V4Z',
  pause: 'M7 4h3v16H7V4Zm7 0h3v16h-3V4Z',
  power: 'M18.4 6.6a9 9 0 1 1-12.7 0M12 2v10',
  trash: 'M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14',
  edit: 'M11 4H4v16h16v-7M18.5 2.5a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5Z',
  copy: 'M9 9h10v10H9V9ZM5 15H4V4h11v1',
  external: 'M18 13v6H4V5h6M15 3h6v6M10 14 21 3',
  search: 'm21 21-4.3-4.3M17 10a7 7 0 1 1-14 0 7 7 0 0 1 14 0Z',
  filter: 'M22 3H2l8 9.5V19l4 2v-8.5L22 3Z',
  logout: 'M9 21H5V3h4M16 17l5-5-5-5M21 12H9',
  login: 'M15 3h4v18h-4M10 17l5-5-5-5M15 12H3',
  menu: 'M3 6h18M3 12h18M3 18h18',
  chevronRight: 'm9 18 6-6-6-6',
  chevronDown: 'm6 9 6 6 6-6',
  chevronLeft: 'm15 18-6-6 6-6',
  arrowUp: 'M12 19V5M5 12l7-7 7 7',
  arrowDown: 'M12 5v14M19 12l-7 7-7-7',
  arrowUpRight: 'M7 17 17 7M7 7h10v10',
  arrowDownLeft: 'M17 7 7 17M17 17H7V7',
  key: 'M15.5 9a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Zm-3.5 0L2 19v3h3v-3h3v-3h3l1.5-1.5',
  lock: 'M5 11h14v10H5V11Zm3 0V7a4 4 0 1 1 8 0v4',
  unlock: 'M5 11h14v10H5V11Zm3 0V7a4 4 0 0 1 7.5-2',
  eye: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7-10-7-10-7Zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z',
  eyeOff: 'M2 2l20 20M10.6 10.6A3 3 0 0 0 12 15a3 3 0 0 0 2.4-1.2M6.7 6.7C3.9 8.4 2 12 2 12s4 7 10 7c1.7 0 3.2-.4 4.5-1M9.9 5.2A9.8 9.8 0 0 1 12 5c6 0 10 7 10 7s-.7 1.3-2 2.7',
  save: 'M19 21H5V3h11l3 3v15ZM8 21v-8h8v8M8 3v5h7',
  upload: 'M12 16V4M7 9l5-5 5 5M4 20h16',
  download: 'M12 4v12M7 11l5 5 5-5M4 20h16',
  clock: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Zm0-16v6l4 2',
  link: 'M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1.8 1.8M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1.8-1.8',
  globe: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20ZM2 12h20M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20Z',
  zap: 'M13 2 3 14h8l-1 8 10-12h-8l1-8Z',
  database: 'M12 8c4.4 0 8-1.3 8-3s-3.6-3-8-3-8 1.3-8 3 3.6 3 8 3Zm8-3v14c0 1.7-3.6 3-8 3s-8-1.3-8-3V5m16 7c0 1.7-3.6 3-8 3s-8-1.3-8-3',
  cpu: 'M6 6h12v12H6V6Zm-2 4h2m-2 4h2m14-4h2m-2 4h2M10 4v2m4-2v2m-4 14v2m4-2v2',
  hardDrive: 'M4 4h16v7H4V4Zm0 9h16v7H4v-7Zm12-6h.01M16 16h.01',
  mail: 'M3 6h18v12H3V6Zm0 0 9 7 9-7',
  user: 'M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
  userPlus: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm10-3v6m3-3h-6',
  tag: 'M20.6 13.4 12 22l-9-9V3h10l7.6 7.6a2 2 0 0 1 0 2.8ZM7.5 7.5h.01',
  layers: 'm12 2 10 5-10 5L2 7l10-5Zm10 10-10 5-10-5m20 5-10 5-10-5',
  maximize: 'M8 3H5a2 2 0 0 0-2 2v3m13-5h3a2 2 0 0 1 2 2v3M8 21H5a2 2 0 0 1-2-2v-3m13 5h3a2 2 0 0 0 2-2v-3',
  moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z',
  sun: 'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Zm0-15v2m0 16v2M4.2 4.2l1.4 1.4m12.8 12.8 1.4 1.4M2 12h2m16 0h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
  inbox: 'M22 12h-6l-2 3h-4l-2-3H2m20 0v8H2v-8m20 0-3.5-7H5.5L2 12',
  gauge: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20Zm4-11-4 4'
}

/** Normalised 24x24 stroke icon definitions shared by both consoles. */
export const iconViewBox = '0 0 24 24'
