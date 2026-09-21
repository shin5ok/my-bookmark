# Persistent Bookmark Summary Jobs Implementation Plan

**Goal:** Bookmark saves start a durable summary job in the same Cloud Run service; short or insufficient pages use sandboxed Chromium and up to three direct children.
**Architecture:** Firestore stores the article summary job, progress, and a time-limited worker lease. One process runs the HTTP server and a bounded job scanner. Fetched HTML is inserted into an isolated blank Chromium page with browser networking blocked; any linked page uses the existing pinned public-IP HTTP client.
**Tech Stack:** Go 1.26.8, Firestore, chromedp, headless-shell, chi, Cloud Run instance-based billing.
**Spec:** `docs/superpowers/specs/2026-09-21-bookmark-summary-worker-design.md`

## Global Constraints
- Keep exactly one Cloud Run service; run HTTP and workers in the same Go process.
- Persist jobs and progress in Firestore, across requests and multiple service instances.
- A failed job only returns to the queue after a signed-in owner presses retry.
- Follow direct hyperlinks only; do not follow links on child pages; visit at most three.
- Validate every URL with the pinned-IP public HTTP fetcher; never let Chromium access the network.
- Preserve the maximum five summary bullets.
- Cloud Run uses instance-based billing, min instances 1, CPU 2, memory 2 GiB, and a bounded maximum instance count.

## Tasks
1. **Firestore job model and transactions:** Complete. Added `SummaryJob`, article progress/error/source fields, transactional initial enqueue, explicit owner retry/quota checks, leased claims and cancellation.
2. **Article extraction and Chromium:** Complete. Extract absolute ranked links and final URLs; render fetched HTML in isolated Chromium; fetch up to three direct child pages through the SSRF-checked client.
3. **Async worker:** Complete. The in-process scanner records stages and results, and stops on SIGTERM without requeuing failures.
4. **HTTP, UX, and deploy:** Complete. Bookmark save returns quickly, status polling is read-only, and only the visible retry button creates a retry job. Cloud Run remains one service with instance-based billing and a minimum instance.
5. **Documentation and review:** Complete for code, docs, build and static analysis. Tests and Cloud Run deployment were not run for this iteration.
