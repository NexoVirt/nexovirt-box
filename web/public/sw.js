// Service worker of the installable web app. It only makes Box installable: every request goes
// to the network as usual (no fetch handler), nothing is cached, so no private data stays on the phone.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
