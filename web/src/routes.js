// routes.js — the address bar's two maps, in one file.
//
// `viewURLs` is what the app WRITES when a view becomes active; `legacyRoutes`
// is what it RESOLVES when a deep link arrives (launcher and PCL derive these
// URLs by swapping the API root's yggdrasil path for user/closet and
// user/profile, so a shared or bookmarked address must land on the same view).
// They are data, not behaviour: the pushState watch, popstate and the cold-boot
// route stay in App.vue next to the shell that serves them, and the source gate
// (TestSPARoutesAgreeWithAppMaps) reads both maps from this file.
export const viewURLs = {
  skinlib: '/skinlib',
  help: '/help',
  closet: '/user/closet',
  profile: '/user/profile',
  players: '/user/players',
  dashboard: '/user/dashboard',
  // Three views the nav offers with no address of their own: the URL bar kept
  // saying /user/dashboard while 站内通知 was on screen, so a bookmark or a
  // second-tab open landed somewhere else entirely.
  notifications: '/user/notifications',
  apps: '/user/apps',
  union: '/user/union',
  admin: '/admin',
}

export const legacyRoutes = {
  '/user/closet': 'closet',
  '/user/profile': 'profile',
  '/user/players': 'players',
  '/user/skinlib': 'skinlib',
  '/user/dashboard': 'dashboard',
  '/skinlib': 'skinlib',
  // Readable without a session: the page exists for the player who is stuck
  // outside, so it must be reachable from a shared link before login.
  '/help': 'help',
  '/user/apps': 'apps',
  '/user/notifications': 'notifications',
  '/user/union': 'union',
  '/admin': 'admin',
}
