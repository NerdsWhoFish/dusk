package web_test

import "testing"

func TestContextPreviewModes(t *testing.T) {
	measured := driveHarness(t, 1280, 800, false, []byte(contextHarness))
	if len(measured) != 1 || measured[0].Problem != "" || measured[0].Page != "context modes" {
		t.Fatalf("context mode regression: %+v", measured)
	}
}

const contextHarness = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>context modes</title></head>
<body><iframe id="frame" width="1280" height="800" src="/context"></iframe>
<script>
(async function () {
  const frame = document.getElementById('frame');
  const doc = () => frame.contentDocument;
  const win = () => frame.contentWindow;
  async function until(predicate, message) {
    const deadline = Date.now() + 15000;
    while (!predicate()) {
      if (Date.now() > deadline) throw new Error(message);
      await new Promise(resolve => setTimeout(resolve, 25));
    }
  }
  const output = () => doc().querySelector('.context-preview .eyebrow')?.textContent;
  const text = () => doc().querySelector('.context-document')?.textContent;
  const submit = () => doc().querySelector('.context-scope button[type="submit"]');
  const mode = index => doc().querySelectorAll('.context-mode button')[index].click();
  async function root(value) {
    const input = doc().querySelector('#context-root');
    Object.getOwnPropertyDescriptor(win().HTMLInputElement.prototype, 'value').set.call(input, value);
    input.dispatchEvent(new (win().Event)('input', { bubbles: true }));
    await until(() => input.value === value, 'repository input did not change');
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  try {
    await until(() => text()?.includes('Global Notes'), 'startup missing global notes');
    const startupTokens = doc().querySelector('.context-token-count').textContent;
    mode(1);
    await until(() => submit().disabled, 'refresh without a repository can submit');
    if (!doc().querySelector('#context-repository-required')) throw new Error('required repository helper missing');
    if (!output().includes('Full startup')) throw new Error('mode selection mislabeled old output');
    await root('example/platform');
    await until(() => !submit().disabled, 'valid refresh cannot submit');
    submit().click();
    await until(() => output().includes('Repository refresh'), 'refresh result missing');
    if (text().includes('Global Notes')) throw new Error('refresh retained startup content');
    if (doc().querySelector('.context-token-count').textContent === startupTokens) throw new Error('refresh token estimate did not update');
    if (!doc().querySelector('.context-call').textContent.includes('"mode":"repository"')) throw new Error('refresh call example mislabeled');
    doc().querySelectorAll('.context-tabs button')[1].click();
    await until(() => doc().querySelector('.context-document.raw pre'), 'raw view failed');
    if (text().includes('Global Notes')) throw new Error('raw view returned startup content');
    doc().querySelector('.context-panel-head').click();
    await until(() => doc().querySelector('[aria-label="Global startup instructions"]'), 'startup editor mislabeled');
    if (!doc().querySelector('.context-policy').textContent.includes('Repository refreshes omit')) throw new Error('editor scope not explained');
    mode(0);
    await root('fail/repository');
    if (!output().includes('Repository refresh')) throw new Error('input changes mislabeled old result');
    submit().click();
    await until(() => doc().querySelector('[role="alert"]'), 'server error not shown');
    if (!output().includes('Repository refresh')) throw new Error('failed request replaced old result mode');
    await root('example/platform');
    submit().click();
    await until(() => output().includes('Full startup') && text().includes('Global Notes'), 'startup retry failed');
    await fetch('/__measured', { method: 'POST', body: JSON.stringify([{ page: 'context modes' }]) });
  } catch (error) {
    await fetch('/__measured', { method: 'POST', body: JSON.stringify([{ page: 'context modes', problem: String(error) }]) });
  }
})();
</script></body></html>`
