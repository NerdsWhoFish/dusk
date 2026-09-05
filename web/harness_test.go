package web_test

import (
	"encoding/json"
	"fmt"
)

// harnessPath is where the browser is pointed. It is not the app: it is a page
// holding the app in a frame sized to one row of the matrix.
const harnessPath = "/__matrix"

// reportPath is where the harness posts what it measured.
const reportPath = "/__measured"

// A page of the app, and the selector that proves it finished rendering rather
// than merely finished loading.
type route struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Rendered string `json:"rendered"`

	// Click is followed before measuring, which is how a panel that is closed
	// until somebody opens it gets measured at all.
	Click   string `json:"click,omitempty"`
	Clicked string `json:"clicked,omitempty"`
}

// A control that failed the touch target minimum, named so a failure says which
// one rather than that something on the page was small.
type target struct {
	Selector string  `json:"selector"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

// What one page measured at one viewport.
type measurement struct {
	Page string `json:"page"`

	// Confirmations, not results: Chrome clamps a headless window to a minimum
	// width, so one asked for 320 lays out at 500 and reports overflow that is
	// not there. Nothing below is worth reading unless these are what was asked.
	InnerWidth  int `json:"innerWidth"`
	ClientWidth int `json:"clientWidth"`

	Coarse bool `json:"coarse"`

	DocScrollWidth  int `json:"docScrollWidth"`
	BodyScrollWidth int `json:"bodyScrollWidth"`

	// Overflowing names what sticks out past the viewport without a scroller of
	// its own, which is the diagnosis for the two widths above.
	Overflowing []string `json:"overflowing"`

	// ContainedOverflow names descendants that escape an analytics panel even
	// when an ancestor clips them before they can widen the page.
	ContainedOverflow []string `json:"containedOverflow"`

	// Counted is how many controls were measured. Zero means the selector
	// matched nothing, which passes every assertion while testing none.
	Counted       int      `json:"counted"`
	Small         []target `json:"small"`
	Accessibility []string `json:"accessibility"`

	Problem string `json:"problem,omitempty"`
}

// harness holds the app in a frame sized to the viewport, because a frame lays
// out at the width it is given and a headless window does not. Measuring it
// from the parent leaves the app served exactly as the binary serves it.
func harness(width, height int, touch bool, routes []route) []byte {
	pages, err := json.Marshal(routes)
	if err != nil {
		panic(err)
	}

	return fmt.Appendf(nil, harnessHTML, width, height, pages, touch, reportPath)
}

const harnessHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>matrix</title>
<style>html,body{margin:0;padding:0}iframe{border:0;display:block}</style></head>
<body>
<iframe id="frame" width="%d" height="%d"></iframe>
<script>
(function () {
  var frame = document.getElementById('frame');
  var pages = %s;
  var touch = %t;
  var report = %q;
  var patience = 15000;

  // Anything reachable by tap, click or keyboard. ADR-0025 asks 44 by 44 of
  // these on a touch viewport.
  var CONTROLS = 'a[href], button, input, select, textarea, summary, [role="button"], [role="menuitem"]';

  // A link inside a sentence cannot be 44 pixels wide without breaking the
  // sentence, which is why WCAG 2.5.8 exempts inline targets. Height still
  // applies to these; only the width rule is lifted.
  var INLINE = '.prose a, .attrs a, .visit';

  function wait(ms) { return new Promise(function (go) { setTimeout(go, ms); }); }

  function painted() {
    return new Promise(function (go) {
      requestAnimationFrame(function () { requestAnimationFrame(go); });
    });
  }

  function load(path) {
    return new Promise(function (go, stop) {
      var timer = setTimeout(function () { stop(new Error('timed out loading ' + path)); }, patience);
      frame.onload = function () { clearTimeout(timer); go(); };
      frame.src = path;
    });
  }

  async function settle(selector) {
    var until = Date.now() + patience;
    for (;;) {
      var doc = frame.contentDocument;
      if (doc && doc.querySelector(selector)) { return; }
      if (Date.now() > until) { throw new Error('nothing ever matched ' + selector); }
      await wait(25);
    }
  }

  function describe(el) {
    var classes = (el.getAttribute('class') || '').trim();
    return el.tagName.toLowerCase() + (classes ? '.' + classes.split(/\s+/).join('.') : '');
  }

  // Whether el sits inside something that scrolls on its own, which is where
  // ADR-0025 puts wide content. An overflow of hidden deliberately does not
  // count: that ADR wants the layout not to overflow, not the evidence hidden.
  function scrolls(win, el) {
    for (var at = el.parentElement; at; at = at.parentElement) {
      var overflow = win.getComputedStyle(at).overflowX;
      if (overflow === 'auto' || overflow === 'scroll') { return true; }
    }
    return false;
  }

  function measure(name) {
    var win = frame.contentWindow;
    var doc = win.document;
    var viewport = doc.documentElement.clientWidth;
    var accessibility = [];
    if (doc.querySelectorAll('main').length !== 1 || doc.querySelector('main main')) {
      accessibility.push('page must contain one main landmark');
    }
    if (name === 'landing') {
      var level = 0;
      doc.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach(function (heading) {
        var next = Number(heading.tagName.slice(1));
        if (next > level + 1) accessibility.push('heading jumps to ' + heading.tagName + ': ' + heading.textContent);
        level = next;
      });
      var graph = doc.querySelector('.graph-canvas');
      if (graph && (graph.getAttribute('role') !== 'img' || !graph.getAttribute('aria-label'))) {
        accessibility.push('graph requires an accessible image role and name');
      }
    }
    var probe = doc.createElement('span');
    probe.style.color = 'var(--muted)';
    doc.body.appendChild(probe);
    var muted = win.getComputedStyle(probe).color;
    probe.remove();
    function rgb(color) { return (color.match(/[\d.]+/g) || []).map(Number); }
    function mix(front, back, alpha) { return front.slice(0, 3).map(function (v, i) { return v * alpha + back[i] * (1 - alpha); }); }
    function luminance(color) {
      var linear = color.map(function (value) {
        value /= 255;
        return value <= 0.04045 ? value / 12.92 : Math.pow((value + 0.055) / 1.055, 2.4);
      });
      return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
    }
    doc.querySelectorAll('main *').forEach(function (element) {
      var style = win.getComputedStyle(element);
      if (style.color !== muted || !element.getBoundingClientRect().height) return;
      if (!Array.from(element.childNodes).some(function (node) { return node.nodeType === 3 && node.textContent.trim(); })) return;
      var ancestors = [];
      var opacity = 1;
      for (var at = element; at; at = at.parentElement) {
        var current = win.getComputedStyle(at);
        opacity *= Number(current.opacity);
        ancestors.unshift(rgb(current.backgroundColor));
      }
      var background = [34, 33, 44];
      ancestors.forEach(function (color) { background = mix(color, background, color.length === 4 ? color[3] : 1); });
      var foreground = mix(rgb(style.color), background, opacity);
      var front = luminance(foreground), back = luminance(background);
      var ratio = (Math.max(front, back) + 0.05) / (Math.min(front, back) + 0.05);
      if (ratio < 4.5) accessibility.push(describe(element) + ' muted contrast ' + ratio.toFixed(2));
    });

    var overflowing = [];
    var all = doc.querySelectorAll('body *');
    for (var i = 0; i < all.length && overflowing.length < 8; i++) {
      var el = all[i];
      var box = el.getBoundingClientRect();
      if (box.width < 1 && box.height < 1) { continue; }
      if (box.left >= -0.5 && box.right <= viewport + 0.5) { continue; }
      if (scrolls(win, el)) { continue; }
      overflowing.push(describe(el) + ' spans ' + Math.round(box.left) + ' to ' + Math.round(box.right));
    }

	var containedOverflow = [];
	var panels = doc.querySelectorAll('.analytics-panel');
	for (var p = 0; p < panels.length && containedOverflow.length < 8; p++) {
	  var panel = panels[p];
	  var boundary = panel.getBoundingClientRect();
	  var descendants = panel.querySelectorAll('*');
	  for (var d = 0; d < descendants.length && containedOverflow.length < 8; d++) {
	    var descendant = descendants[d];
	    var descendantBox = descendant.getBoundingClientRect();
	    if (descendantBox.width < 1 && descendantBox.height < 1) { continue; }
	    if (descendantBox.left >= boundary.left - 0.5 && descendantBox.right <= boundary.right + 0.5) { continue; }
	    if (scrolls(win, descendant)) { continue; }
	    containedOverflow.push(describe(panel) + ' contains ' + describe(descendant) + ' spanning ' + Math.round(descendantBox.left) + ' to ' + Math.round(descendantBox.right) + ' outside ' + Math.round(boundary.left) + ' to ' + Math.round(boundary.right));
	  }
	}

    var counted = 0;
    var small = [];
    var controls = doc.querySelectorAll(CONTROLS);
    for (var j = 0; j < controls.length; j++) {
      var control = controls[j];
      var rect = control.getBoundingClientRect();
      if (rect.width < 1 && rect.height < 1) { continue; }
      counted++;
      if (!touch) { continue; }

      var wide = control.matches(INLINE) || rect.width >= 43.5;
      if (rect.height >= 43.5 && wide) { continue; }
      small.push({ selector: describe(control), width: rect.width, height: rect.height });
    }

    return {
      page: name,
      innerWidth: win.innerWidth,
      clientWidth: viewport,
      coarse: win.matchMedia('(pointer: coarse)').matches,
      docScrollWidth: doc.documentElement.scrollWidth,
      bodyScrollWidth: doc.body.scrollWidth,
      overflowing: overflowing,
	  containedOverflow: containedOverflow,
      counted: counted,
      small: small,
      accessibility: accessibility
    };
  }

  async function run() {
    var measured = [];
    for (var i = 0; i < pages.length; i++) {
      var page = pages[i];
      await load(page.path);
      await settle(page.rendered);
      if (page.click) {
        await settle(page.click);
        frame.contentDocument.querySelector(page.click).click();
        await settle(page.clicked || page.click);
      }
      await painted();
      measured.push(measure(page.name));
    }
    return measured;
  }

  run().then(function (measured) {
    send(measured);
  }).catch(function (err) {
    send([{ page: 'harness', problem: String(err && err.message || err) }]);
  });

  function send(body) {
    fetch(report, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
  }
})();
</script>
</body>
</html>`
