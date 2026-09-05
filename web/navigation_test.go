package web_test

import "testing"

func TestLandingHistoryAndPreviewNavigation(t *testing.T) {
	measured := driveHarness(t, 1280, 800, false, []byte(navigationHarness))
	if len(measured) != 1 || measured[0].Problem != "" || measured[0].Page != "history restored" {
		t.Fatalf("landing history regression: %+v", measured)
	}
}

const navigationHarness = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>navigation</title></head>
<body><iframe id="frame" width="1280" height="800" src="/"></iframe>
<script>
(async function () {
  const frame = document.getElementById('frame');
  const doc = () => frame.contentDocument;
  const win = () => frame.contentWindow;
  const pause = () => new Promise(resolve => setTimeout(resolve, 25));
  async function until(predicate, message) {
    const deadline = Date.now() + 15000;
    while (!predicate()) {
      if (Date.now() > deadline) throw new Error(message + ' at ' + win().location.href + ': ' + doc().body.innerText.slice(0, 500));
      await pause();
    }
  }
  const exists = selector => until(() => doc().querySelector(selector), 'missing ' + selector);
  function navigate(path) {
    win().history.pushState(null, '', path);
    win().dispatchEvent(new (win().PopStateEvent)('popstate'));
  }
  async function fill(value) {
    const input = doc().querySelector('#q');
    Object.getOwnPropertyDescriptor(win().HTMLInputElement.prototype, 'value').set.call(input, value);
    input.dispatchEvent(new (win().Event)('input', { bubbles: true }));
    await until(() => win().history.state?.duskLanding?.query === value, 'query was not preserved');
  }
  async function backToSearch() {
    win().history.back();
    await exists('#q');
    await until(() => doc().querySelector('#q').value === 'checkout', 'Back lost the query');
    await exists('main > .rows .row:nth-child(2)');
  }
  try {
    await exists('.chip.kind-service');
    doc().querySelector('.chip.kind-service').click();
    await exists('.chip.kind-service[aria-pressed="true"]');
    await fill('checkout');
    await exists('main > .rows .row:nth-child(2)');
    doc().querySelector('main > .rows .row:first-child').click();
    await exists('.identity');
    await backToSearch();
    win().history.forward();
    await exists('.identity');
    await backToSearch();
    doc().querySelector('main > .rows .row:nth-child(2)').click();
    await exists('.note-identity');
    await backToSearch();
    await new Promise(resolve => {
      frame.onload = resolve;
      win().location.reload();
    });
    await exists('#q');
    await until(() => doc().querySelector('#q').value === 'checkout', 'Reload lost the query');
    await fill('');
    await exists('.chip.kind-service[aria-pressed="true"]');
    await exists('main > .rows .row');
    doc().querySelector('main > .rows .row').click();
    await exists('.identity');
    win().history.back();
    await exists('.chip.kind-service[aria-pressed="true"]');
    await fill('checkout');
    doc().querySelector('.brand').click();
    await until(() => doc().querySelector('#q')?.value === '', 'new landing entry reused the old query');
    await until(() => win().history.state?.duskLanding?.query === '', 'new landing entry did not settle');
    win().history.back();
    await until(() => doc().querySelector('#q')?.value === 'checkout', 'Back between landing entries lost search');
    win().history.forward();
    await until(() => doc().querySelector('#q')?.value === '', 'Forward between landing entries reused search');
    await fill('checkout');
    doc().querySelector('.menu-button').click();
    await exists('[role="menuitem"]');
    const plugins = Array.from(doc().querySelectorAll('[role="menuitem"]')).find(el => el.textContent.includes('Plugins'));
    if (!plugins) throw new Error('plugins menu item missing');
    plugins.click();
    await exists('.row.plugin');
    await backToSearch();
    const preview = 'refs/pull/example/platform/7/head';
    navigate('/?ref=' + encodeURIComponent(preview));
    await exists('.preview-banner');
    await until(() => doc().querySelector('h1')?.textContent === 'Preview catalog', 'preview home read used live data');
    await until(() => !doc().querySelector('.search-modes'), 'AI search is unavailable in previews');
    await fill('checkout');
    await exists('main > .rows .row:nth-child(2)');
    await until(() => doc().querySelector('.row-title')?.textContent === 'Preview checkout API gateway', 'preview search used live data');
    doc().querySelector('main > .rows .row:first-child').click();
    await until(() => doc().querySelector('.identity h1')?.textContent === 'Preview checkout API gateway', 'preview entity read or cache used live data');
    if (new URLSearchParams(win().location.search).get('ref') !== preview) throw new Error('entity navigation dropped preview scope');
    if (doc().querySelector('.entity-edit') || doc().querySelector('.note-close')) throw new Error('preview exposes write controls');
    const entityPath = win().location.pathname;
    await backToSearch();
    if (new URLSearchParams(win().location.search).get('ref') !== preview) throw new Error('Back dropped preview scope');
    doc().querySelector('main > .rows .row:nth-child(2)').click();
    await exists('.note-identity');
    if (new URLSearchParams(win().location.search).get('ref') !== preview) throw new Error('note navigation dropped preview scope');
    await backToSearch();
    navigate('/context?ref=' + encodeURIComponent(preview));
    await until(() => doc().body.textContent.includes('configuration are available in the live catalog'), 'preview exposed live context configuration');
    if (doc().querySelector('.context-page')) throw new Error('preview mounted the context editor');
    navigate(entityPath);
    await until(() => doc().querySelector('.identity h1')?.textContent === 'Checkout API gateway', 'live entity cache reused preview data');
    if (doc().querySelector('.preview-banner')) throw new Error('preview banner survived returning to live data');
    navigate('/');
    await until(() => doc().querySelector('h1')?.textContent === 'The catalog', 'live home cache reused preview data');
    await fetch('/__measured', { method: 'POST', body: JSON.stringify([{ page: 'history restored' }]) });
  } catch (error) {
    await fetch('/__measured', { method: 'POST', body: JSON.stringify([{ page: 'history', problem: String(error) }]) });
  }
})();
</script></body></html>`
