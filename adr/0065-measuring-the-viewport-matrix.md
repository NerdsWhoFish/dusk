# 65. The viewport matrix is measured in a browser, in a frame, by a Go test

Date: 2026-08-16

## Status

Accepted

## Context and Problem Statement

[ADR-0025](0025-responsive-ui.md) made the viewport matrix the definition of done and said the tests assert behaviour rather than pixels.
It did not say what runs them, and nothing did.

The matrix has been run by hand against a real catalog and it passes.
That is worth less than it sounds.
A check somebody remembers to perform is a check that stops happening the week somebody is busy, and the failure it is guarding against is invisible on the machine the work is done on: the `1fr` defect that broke every phone was in the single-column rule while the two-column rule was correct, so a laptop showed nothing wrong for as long as it took somebody to open the page on a phone.

Two facts about measuring a layout are already written down here, both learned expensively.

Chrome headless lays out wider than it captures when the window is sized from the command line, so its screenshots crop instead of reflowing and read as overflow that is not there.
The mitigation recorded with it is to measure `document.body.scrollWidth` against `clientWidth` in the page, and to trust only a browser whose `innerWidth` has been confirmed.

And [ADR-0025](0025-responsive-ui.md) rejected screenshot comparison outright, because its failures are usually noise and a check people learn to approve without reading is worse than no check.

So the question is narrow: what can render this application at 320 CSS pixels, on a runner with no display, and be believed.

## Considered Options

1. **A browser automation library as a frontend dependency**: Playwright or Puppeteer, with its own test runner and its own browser download.
2. **A DOM implementation without a browser**: jsdom or happy-dom, in a JavaScript unit test.
3. **A Go test driving a headless browser directly**, measuring inside the page.

And, once option 3 was chosen, how the viewport is set:

1. **Size the browser window** with `--window-size`.
2. **Drive the browser over the DevTools Protocol** and call `Emulation.setDeviceMetricsOverride`.
3. **Load the application in a frame** sized to the viewport, and measure it from the parent.

## Decision Outcome

Chosen: **a Go test**, driving **a headless Chrome**, with the application **in a frame**.

### The measurement is a number the page computed about itself

Every assertion is something the document says about its own layout:

- `document.documentElement.scrollWidth` and `document.body.scrollWidth` against `clientWidth`, which is ADR-0025's "no horizontal page scroll, ever".
- The bounding box of every control, against the 44 pixel minimum.

Nothing is compared to an image, so [ADR-0025](0025-responsive-ui.md)'s rejection of screenshot diffing stands untouched, and a restyle that changes every colour on the page changes no assertion here.

An overflow failure also names what stuck out, with the ancestor chain, because "the document is 601px wide in a 320px viewport" is a symptom and `section.block spans 16 to 601` is the defect.

### The viewport is a frame, because a headless window will not go narrow

`--window-size=320,568` was measured on Chrome 151 and produced a document that laid out at **500 pixels**.
The window has a platform minimum and the request is silently clamped.
That is the recorded fact about `--window-size`, in its sharpest form: below about 500 pixels the browser is not laying out at the width it was asked for, which is exactly the range the matrix exists to cover.

A frame has no such floor.
An `iframe` sized to 320 by 568 gives the document inside it `innerWidth` of exactly 320, `clientWidth` of exactly 320, and width media queries that resolve against 320.
Measured at 320, 430 and 1440: exact at all three, with the `48rem` breakpoint firing at 1440 and not at the other two.

It has a second benefit that decided it over the alternatives even before the clamp was found.
The parent frame does the measuring, so **nothing is injected into the page under test**: the application is served byte for byte as the binary serves it, rather than as the binary serves it plus a script that could itself change the layout.

### What was asked for is confirmed, and a mismatch fails

The clamp is the reason for a rule rather than a workaround.
Before any measurement is read, the test checks that the page reports the width it was asked for and the pointer type the row calls for.
A browser that quietly laid out somewhere else, or an emulation that stopped taking, fails the test naming what it actually did.

This is the same instinct as the `DUSK_TEST_NO_SKIP` flag already in CI: a check that has silently stopped checking is worse than one that is missing, because it reports success.

For the same reason each page reports how many controls it measured, and zero is a failure.
A selector that matches nothing passes every size assertion under it.

### Touch is emulated with Blink settings rather than the DevTools Protocol

The 44 pixel rule is on touch viewports, and the CSS branches on `@media (pointer: coarse)`.
A headless browser is a mouse until told otherwise, so asserting the rule without emulating touch would assert a rule the stylesheet deliberately does not apply.

`--blink-settings=primaryPointerType=2,availablePointerTypes=2,primaryHoverType=1,availableHoverTypes=1` produces `pointer: coarse` and `hover: none`, confirmed in the page.
It is a flag rather than an API, so the confirmation is not optional.

### The catalog under the matrix is a fixture

The API is stubbed by the test rather than being a running Dusk.
A real catalog renders whatever it happens to contain, which is how a matrix run by hand passes while a phone breaks: the ref that overflows has to be in the catalog on the day somebody looks.

The fixture holds the page at its hardest on purpose: the longest ref, a kind name nobody would choose, a six column declared table, a markdown table, a code fence, a repository name that does not fit, and a plugin that is failing.

### A missing browser skips, and CI says a skip is a failure

`DUSK_TEST_NO_SKIP` already exists in CI for exactly this, so a contributor with no Chrome is not blocked and a runner without one cannot go green.

## Consequences

### Good

- The matrix is a property of every push rather than a habit, which is what [ADR-0025](0025-responsive-ui.md) asked for and did not get.
- Reintroducing the historical `1fr` defect fails the suite at 320, 390 and 430 and passes at 768 and above, which is the shape of the original bug exactly. The test was verified by breaking the thing it exists to catch.
- No new frontend dependency, no second test runner, and no browser download: `go test` is still the whole story.
- Nothing is injected into the page under test, so the thing measured is the thing shipped.
- The failure names the element, so a red build is a fix rather than an investigation.

### Bad

- **The suite now needs a browser on the runner**, which is a class of dependency this repository did not have. It is preinstalled on the runner image in use, and that is a bet on somebody else's image rather than a thing this repository controls.
- **It costs about eighteen seconds**, in eleven browser launches. The two tests measure independently rather than sharing a run, which is twice the browser time in exchange for each test standing on its own.
- **A route is measured as it first renders.** A panel that opens on a tap is measured only where the harness is told to open one, which today is the menu. The plugin configuration form and the action parameter form are not reached, so ADR-0025's "every view" is being read as "every route".
- **The fixture can fall behind the wire shapes it imitates.** A field renamed in `internal/server` and not here fails as a page that never settles, which points at the harness rather than at the field that moved.
- **Blink settings are a flag, not a contract.** Chrome may rename or drop them. The in-page confirmation makes that loud rather than silent, but it is still a break waiting to happen on somebody else's release schedule.
- **Scrollbars are hidden**, so the viewport is exactly the width asked for on every platform. That is faithful to a phone, which has overlay scrollbars, and about fifteen pixels optimistic for a desktop that does not.
- A browser test is slower and flakier than a unit test, which [ADR-0025](0025-responsive-ui.md) already accepted. The first launch in a cold process was seen to take far longer than the rest, so the wait is generous rather than tight.

### Rejected because

- **Playwright or Puppeteer** was rejected on what it costs to carry rather than on what it does. It is a browser download and a second test runner in a repository where `make check` is `go test`, for one file, and CI would grow a toolchain that nothing else uses. Its viewport API would also have papered over the window clamp instead of surfacing it, which would have been convenient and would have left the same fact unlearned for the next person to hit from a different direction.
- **jsdom or happy-dom** was rejected because neither implements layout. `scrollWidth` is always zero, so every assertion here passes on every page forever. That is not a cheaper test, it is the failure mode [ADR-0025](0025-responsive-ui.md) rejected screenshots for, arrived at from the other side: a check that cannot fail.
- **Driving the DevTools Protocol** was rejected because it needs a WebSocket client, which means `golang.org/x/net` or a protocol library, against [ADR-0017](0017-engineering-policy.md)'s standard library only rule for tests. It is the right answer if the frame ever stops being enough, and it is the only way to emulate a device properly rather than approximately.
- **Sizing the browser window** was rejected because it was measured doing the wrong thing: 320 laid out at 500. Everything below the clamp would have been tested at the clamp, silently, which is the false pass this whole record exists to prevent.
- **Screenshot comparison** was rejected by [ADR-0025](0025-responsive-ui.md) and nothing here is new information.
