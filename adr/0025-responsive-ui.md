# 25. Mobile and desktop are both first class, verified by a viewport matrix

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk answers "where is this thing" and "how do I do this again".
Both questions get asked away from a desk, often precisely when something is broken and the person asking is holding a phone.

That is not hypothetical for this project.
Its first operator drives work from a phone regularly, so a desktop-only catalog would be unavailable exactly when it is most useful.

"Responsive" as an aspiration reliably degrades.
Someone builds a wide table, it is tested at 1440 pixels, and nobody sees the horizontal scrollbar on a phone for three months.
Without a definition and a mechanism, mobile support becomes a bug report rather than a property.

Some of this product is genuinely hard on a small screen, and that has to be acknowledged rather than wished away: entity tables with many columns, relation graphs, side-by-side pull request diffs, and YAML blocks.

## Considered Options

1. **Desktop only**, with mobile treated as best effort.
2. **A separate mobile interface**, either a distinct route or a native application.
3. **One responsive interface** covering both, with a defined viewport matrix and tests.

## Decision Outcome

Chosen: **option 3**.

One codebase, one URL, no device sniffing, and no feature that exists on one form factor and not the other.

### The viewport matrix is the definition

"Works on mobile" is otherwise unfalsifiable, so it is fixed as a list:

| Name | Viewport | Represents |
| --- | --- | --- |
| `phone-sm` | 375 x 667 | Smallest phone still worth supporting |
| `phone` | 390 x 844 | Typical modern phone |
| `tablet` | 768 x 1024 | Tablet portrait, narrow split view |
| `desktop` | 1280 x 800 | Laptop |
| `wide` | 1920 x 1080 | External display |

**A view is not done until it passes at every entry.** That is the enforceable form of this decision, and everything else here is detail.

### CSS is mobile first

Base styles target the smallest viewport and `min-width` queries add capability upward.

Mobile first forces prioritisation: what survives at 375 pixels is what actually matters, and desktop then has room rather than mobile having a deficit.

### What each viewport must satisfy

- **No horizontal page scroll, ever.** Wide content scrolls inside its own container, never the body.
- **Interactive targets at least 44 by 44 pixels** on touch viewports.
- **No hover-only affordance.** Anything reachable by hover is reachable by tap or focus.
- **Readable without zooming**, with a minimum body size and no viewport-scale lock.
- **Keyboard reachable**, with visible focus, since a phone keyboard and a desktop keyboard both matter.

### The hard cases have declared answers

These are the views that will otherwise quietly break, so their behaviour is decided now rather than improvised:

- **Tables** collapse to stacked cards below `tablet`, rather than scrolling sideways. A table that scrolls off screen hides the column that mattered.
- **Relation graphs** fall back to a nested list on touch viewports. A force-directed graph on a phone is a demo, not a tool.
- **Pull request diffs** switch from side-by-side to unified below `tablet`.
- **Code and YAML blocks** scroll inside themselves, with the container clipping rather than the page.

### Tests assert behaviour, not pixels

Every view is rendered at every viewport in the matrix, and the test asserts:

- `document.documentElement.scrollWidth` does not exceed the viewport width.
- Every interactive element meets the touch target minimum on touch viewports.
- The view's key content is present and visible, not merely in the DOM.

**Screenshot comparison is explicitly rejected.** Visual regression breaks on font rendering, on a colour change, and on every runner difference, which trains people to approve diffs without reading them. That is worse than no test, because it converts a failing check into a ritual.

This is the same rule as [ADR-0017](0017-engineering-policy.md): assert the observable result, not the implementation. "No horizontal scroll at 375 pixels" is a result. A pixel hash is not.

## Consequences

### Good

- Mobile support becomes a property with a definition, rather than a bug report someone files months later.
- A fixed matrix means "does this work on a phone" has an answer that does not depend on who is asking.
- Mobile-first CSS forces the content hierarchy to be decided deliberately, which improves the desktop view too.
- Behavioural assertions survive restyling, so the suite does not become an obstacle to changing the design.
- Declaring the hard cases up front means tables, graphs, and diffs get designed once instead of patched per report.

### Bad

- Every view now costs five renders in CI, which is real time on every pull request.
- Two layout modes for tables, graphs, and diffs is more code, and the mobile variant will get less attention because it is used less by whoever is building it.
- The matrix will be wrong eventually. Fixing a list of viewports means it drifts from real devices, and expanding it makes the suite slower.
- Declining screenshot tests means genuine visual regressions, an overlapping element or an invisible-on-invisible colour, will pass. Those are caught by review or not at all.
- Browser-driven tests are slower and flakier than unit tests, and they will occasionally fail for reasons unrelated to the change.

### Rejected because

- Desktop only was rejected because the product's core use case happens away from a desk, and treating that as best effort means it does not work.
- A separate mobile interface was rejected as two products. Feature parity would decay immediately, and every change would need building twice.
- Screenshot comparison was rejected because its failures are usually noise, and a check people learn to approve without reading is worse than no check.

## Amendments

### 2026-08-12: accidental zoom is a touch-action problem, not a scale-lock one

"No viewport-scale lock" above stands, and this records why it was nearly lost.

A page that zooms when you did not mean it to reads as a document somebody forgot to finish rather than an application, and the obvious fix is `user-scalable=no`.
That fix is wrong twice over: it is a WCAG 1.4.4 failure, and iOS Safari has ignored the tag since iOS 10, so on the platform this is most read from it does nothing at all.

The actual cause is **double-tap to zoom** on controls, and the cure is `touch-action: manipulation` on links, buttons, inputs and rows.
That removes double-tap zoom and the 300ms tap delay, leaving deliberate pinch zoom untouched.

Two other things keep accidental zoom away, and both are now rules rather than accidents:

- **Inputs are at least 16px.** Below that, iOS zooms the page when a field takes focus, which is the other common source of "why did it zoom".
- **The layout never overflows horizontally**, so there is nothing to pan into by mistake. This is enforced by not overflowing rather than by `overflow-x: hidden`, which only hides the evidence and, on a zoomed phone, prevents panning to the content it hid.
