import { JSDOM } from 'jsdom';

export function installDOM() {
  const dom = new JSDOM('<html><body></body></html>', { url: 'http://localhost/api/checkout?secret=private' });
  const originals = new Map();
  for (const key of ['window', 'document', 'navigator', 'location', 'sessionStorage', 'localStorage', 'XMLHttpRequest', 'Event', 'ErrorEvent', 'addEventListener', 'removeEventListener']) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: key.endsWith('EventListener') ? dom.window[key].bind(dom.window) : dom.window[key] });
  }
  return {
    window: dom.window,
    async restore() {
      // OTel's scheduled callback still needs the DOM after disposal.
      await new Promise(resolve => setTimeout(resolve, 1100));
      dom.window.close();
      for (const [key, descriptor] of originals) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else delete globalThis[key];
      }
    },
  };
}
