# Jan Taxi — audit and implementation notes, 29 October 2026

Baseline inspected: commit `b7378a2`. Scope: contacts, manual final destination,
driver document verification and related security. Existing UI components and
tokens, order lifecycle, wallet calculations and historical records are retained.
Production was not accessed or deployed. Real Telegram device testing belongs to
the operator and customer.

## 18.1 Executive summary

The platform already has transactional order acceptance, pickup-based PostGIS
matching, private document storage, application rounds, permission-based admin
review and a durable notification outbox. Extend these rather than replace them.

Confirmed policy decisions: a passenger explicitly selects the fare for manual B
inside the active administrator tariff's minimum/maximum/step; existing commission
rules apply. Confident complete identity matches may auto-approve; uncertainty,
conflicts or processing failure go to administrators. Document numbers have unique
ownership independently of FIO. Identity cards and driving licences are supported
document types. Only one OCR task may execute globally at a time.

## 18.2 Confirmed issues and root causes

References below identify baseline functions; source line numbers may move as
the implementation is applied.

| Severity | Evidence and root cause | Minimal change |
|---|---|---|
| Medium | `driver/miniapp/src/components/driver/TripContacts.vue`, native `tel:` anchor: no passenger copy fallback or WhatsApp action. | Keep contact appearance; add independent HTTPS WhatsApp action and copy fallback. |
| Medium | `driver/miniapp/src/components/ui/JSheet.vue:onPointerDown/onPointerMove/suppressNextClick`: links/buttons can initiate a sheet drag; movement of 6 px activates click suppression. | Exempt contact action group with the existing `data-sheet-no-drag` hook. |
| High privacy | `dispatch/accept.go:TripCardParams` snapshots a phone; `notifications/worker.go:deliver` previously did not recheck assignment at send/retry. | Cancel superseded pending cards and fence bounded sends against current assignee, active status and assignment timestamp. |
| High feature blocker | `routing/preview.go:validateRoute`, `pricing/quote.go:CreateQuote`, `orders/create.go:insertOrder`, migration 00001: all stops and full route totals required coordinates/numeric values. | Add explicit manual final-stop mode, nullable persistence/wire metrics and an approved manual quote branch. |
| High integration risk | `passenger/miniapp/src/api/parse.ts:num` defaulted null coordinates/metrics to zero. | Preserve NULL; never fabricate (0,0), distance or duration. |
| Medium | Driver `features/navigation/twogis.ts:planNavigation` rejected the entire plan when B was invalid; driver/admin maps assumed all stops had coordinates. | Navigate only real coordinate prefixes, retain pickup navigation and show the manual landmark. |
| Medium | Offer template validation and CSV scans expected full-route metrics. | Omit unknown metrics safely in bot messages and export blank values. |
| High feature blocker | Registration collected no entered IIN/document number and had no OCR jobs/results/identity ownership. | Add protected inputs, per-application jobs/results and transactional ownership claims. |
| Medium | ID uploads were images only; receipt PDF page enumeration truncated rather than proving an actual page limit. | Add a separate bounded ID PDF path with explicit page-count rejection; retain receipt rules. |
| Medium compatibility | Suspension only handled approved drivers. New identity requirements could strand pre-rollout pending rounds. | Preserve historical manual approval; track previous status for pending/rejected suspension and restoration. |
| Medium future privacy | Admin application snapshots are returned to roles with `drivers.view`. | Keep new sensitive identity/result values in protected separate storage, gated by `documents.view`, with audited sensitive access. |

The exact original mobile dialer failure is not proven without the affected
Telegram/client/device versions. Gesture interception is confirmed in code and
tests; WebView telephone behavior still requires the operator's device check.

Baseline evidence lines at `b7378a2`: `TripContacts.vue:34,67,76`;
`JSheet.vue:234–325`; `admin/backend/internal/dispatch/accept.go:340–424`;
`admin/backend/internal/notifications/worker.go:451–485`;
`admin/backend/internal/routing/preview.go:399–445`;
`admin/backend/internal/pricing/quote.go:157–159`;
`admin/backend/internal/orders/create.go:282–396`;
`admin/backend/migrations/00001_init.sql:202–224,447–472`;
`passenger/miniapp/src/api/parse.ts:58–60,206–215,300–322`;
`driver/miniapp/src/features/navigation/twogis.ts:96–112`;
`admin/backend/internal/moderation/detail.go:150–164`;
`admin/backend/internal/platform/storage/pdf.go:48,61–92`;
`admin/backend/internal/platform/storage/fs.go:180–183`.

## 18.3 Architecture analysis

Passenger: HTTP geocoder/GPS/map pin → route preview/owned expiring quote →
transactional order creation, immutable route snapshot and offers → accepted →
arrival/start/intermediate completion → completion/cancellation/dispute.

Matching uses `dispatch/radius.go` and `o.pickup` exclusively. Registry defaults
are 5 km initially, +2.5 km per 60 seconds, maximum 10 km, and location freshness
300 seconds. These are defaults; deployed runtime settings may differ. Approval,
online status, fresh location, vehicle capacity/class, balance, no active ride,
unique offers and the atomic first-winner acceptance remain enforced.

Contacts come from the server-confirmed active-assignee view. Other drivers see
no passenger phone. Direct telephone and WhatsApp links are triggered only by a
tap; WhatsApp uses international digits and never sends a message automatically.
Delivered phone numbers cannot be made unknown again after revocation.

Registration retains `draft/pending/approved/changes_requested/rejected/suspended`.
An immutable application round and an OCR job are queued with the acknowledgment
in the submission transaction. OCR substatus is separate. The worker uses the
same guarded approval effects as admin review, including once-only wallet/bonus
behavior; stale jobs cannot override a new round or an administrator.

## 18.4 Database analysis and rollback

PostgreSQL 17/PostGIS 3.5; migrations are embedded Goose SQL. New versions:
`00010_manual_destinations.sql` and `00011_driver_verification.sql`.

Existing `orders`, `order_stops`, `fare_quotes`, `order_route_snapshots`,
`driver_profiles`, `driver_documents`, `driver_applications`, wallets/ledger,
moderation events and notification tables are reused. Final manual B has both
coordinates SQL NULL; A/intermediates/geocoded B retain valid coordinate pairs.
Unknown full-trip totals are NULL. Existing numerical historical values remain.
Pickup geography/GiST indexes and monetary records remain intact.

Protected identity inputs and OCR results use AES-GCM with an operator-held key;
keyed fingerprints support unique IIN/document ownership. A number is a string:
leading zeros and licence series are preserved. Claims are made only inside a
successful approval/explicit review transaction, not by an arbitrary upload.
Same-driver resubmission reuses ownership; another owner triggers review.
Renewal retains old claims/history. Names are comparison values, never unique.

Queue due/recovery/driver indexes, unique application job and primary ownership
constraints protect concurrency and idempotency. Historical approved drivers
are not forced to re-register or assigned guessed identity values.

Rollback after feature data exists means disabling new writes/OCR/autoapproval
and retaining compatible readers plus additive schema. Guarded Down migrations
refuse to remove live evidence/manual destinations. Restore drills use the
existing database-plus-private-files backup scripts in an isolated database.
No backup restore or destructive rollback was executed on production.

## 18.5 UI changes and localization

Reuse current sheets, controls, colors, spacing and 44 px contact actions.
Passenger search gains a final-B-only manual option and inline field; switching
mode retains pickup, intermediate stops and unrelated choices. Driver contacts
retain Telegram chat and gain WhatsApp/copy; maps and order/admin/history show a
manual landmark label. Registration adds compact identity fields/type and hints;
existing status/admin review panels display verification data and comparisons.

| Meaning | kk | ru | en |
|---|---|---|---|
| Manual option | Мекенжай табылмады ма? Қолмен енгізіңіз | Не нашли адрес? Укажите место вручную | Cannot find the address? Enter it manually |
| Landmark | Жолаушы көрсеткен орын | Место указано пассажиром | Passenger-entered landmark |
| Unknown route | Маршрут есептелмеген | Маршрут не рассчитан | Route not calculated |
| Name hint | Аты-жөніңізді құжатта көрсетілгендей дәл жазыңыз. | Укажите имя и фамилию точно так, как в документе. | Enter your first and last name exactly as shown on your document. |

Manual text reuses NFC/whitespace/control normalization and the existing 200-code-
point address limit. Patronymic is optional. Acknowledgment gives a five-minute
target with an explicit queue/additional-review qualification, not a guarantee.

## 18.6 OCR architecture and resource limits

Initial local engine: Tesseract 5 with only `kaz/rus/eng`; PDF handling uses
Poppler. Text-layer extraction is corroborated with visible OCR and never proves
authenticity. Tesseract fast/best models trade speed for accuracy. PaddleOCR's
CPU multilingual mobile models are an alternative if a broader authorized corpus
demonstrates better field accuracy; its larger runtime was not added.

Submitted examples include separate physical sides, a combined viewer PNG with
an absent patronymic, and a licence illustration. Viewer chrome/file names must
not be treated as document fields. A screenshot is not an original PDF. Layouts
and machine-readable rows vary; missing fields remain unknown.

The isolated worker runs as the same non-root UID as private storage, with a
read-only files mount, no external OCR service, 1 CPU, 768 MiB container memory,
128 MiB temporary storage and 64 PIDs. One global claim gate plus leases/fencing
bounds work to one task; engine threading is one. Default timeout is 60 seconds
(hard maximum 90), lease 120 seconds and retries three with capped backoff.
Output buffers, upload sizes, PDF page count and raster dimensions are bounded.
Shutdown/crash recovery preserves jobs; output/errors/logs omit raw identity data.
Queue saturation durably routes an accepted application to admin review.

OCR confidence is an engine score, not proof of document authenticity. Exact
required fields, verified phone/consent, current application, valid matching IIN,
readable number, no identity/account conflicts and existing completeness rules
are all required for autoapproval. Uncertain transliteration, conflicting labels,
type mismatch or unreadable data goes to administrators, never an automatic ban.
No DOB/sex inference from IIN is used.

The default confidence threshold is conservative and configurable; the small
sample set does not establish a production false-accept rate. The full 4-CPU/
6-GB stack must retain measured headroom: container memory limits are not actual
usage, and existing configured maxima can sum above host RAM.

Sources: [Tesseract models](https://tesseract-ocr.github.io/tessdoc/Data-Files.html),
[thread control](https://tesseract-ocr.github.io/tessdoc/FAQ.html),
[PaddleOCR language models](https://www.paddleocr.ai/latest/en/version3.x/algorithm/PP-OCRv5/PP-OCRv5_multi_languages.html),
[official IIN explanation](https://www.gov.kz/org/migration/press/news/details/132237?lang=ru).

## 18.7 Reviewable implementation groups

1. Contact component/phone helpers, contact notification authorization and tests.
2. Migration 10, stops/quote/order wire and storage, approved manual pricing path.
3. Passenger search/booking/history, driver navigation/maps, admin order views,
   nullable bot/export handling and all three languages.
4. Migration 11, protected identity inputs/ownership, OCR parser/engine/queue,
   guarded shared approval, existing registration/admin/status views and tests.
5. Separate worker image/configuration, opt-in deployment profile, OpenAPI,
   operational/rollback notes and final regression/build checks.

No deployment is part of these code changes. Enable readers before writers.

## 18.8 Testing strategy and boundaries

Baseline: Go tests with database tests excluded, 798 frontend tests and all three
TypeScript checks passed before edits. Implementation checks use a separate local
PostGIS database, synthetic identities and fake notification/OCR transports;
user documents are not committed as fixtures.

Regression coverage includes unauthorized/cancelled/reassigned contacts and
retries, click isolation, nullable persistence/wire/history, normal routes,
manual A/B modes, admin tariff boundaries, 4.9/5.1 and 9.9/10.1 km matching,
commission/idempotency/lifecycle, guarded migrations, field/confidence parsing,
malformed uploads, duplicate ownership, resubmission, stale admin/OCR races,
retry/restart leases, once-only bonus and 20 concurrent registration submissions.
Concurrent worker tests verify peak OCR concurrency one. Real Tesseract resource
observations are separate from synthetic queue stress and from full-stack load.

Operator/customer device tests still cover iOS/Android Telegram, dialer prompts,
WhatsApp/Business/missing app, contact taps at sheet positions and visual layout.

### Automated verification results

- Complete Go suite passed with the race detector, real isolated PostGIS and the
  opt-in OpenAPI route check: `go test -race -p 4 -count=1 ./...` with
  `JT_CHECK_OPENAPI=1` and an isolated `TEST_DATABASE_URL`.
- Passenger 327, driver 419 and admin 111 tests passed: 857 frontend tests total.
- All three app/node TypeScript configurations, production builds and local lint
  passed. The shared-core check verified 82 files. Compose configuration parses.
- The final OCR worker image built successfully with Tesseract 5.3 and Poppler.
- Twenty synthetic concurrent registration submissions returned in approximately
  213 ms in the local test; this is not a production latency measurement. Twenty
  queued jobs across four concurrent callers maintained maximum processing one.

### Local OCR observations on supplied examples

Separate sequential containers used 1 CPU, 768 MiB memory, no network, read-only
original PNGs and bounded temporary storage. Default Tesseract observations:

| Example | Wall time including container startup | CPU time | Cgroup peak memory | Recognized words |
|---|---:|---:|---:|---:|
| Physical front | 0.301 s | 0.162 s | 41.27 MiB | 0 |
| Physical back | 1.272 s | 1.129 s | 47.25 MiB | 12 |
| Both sides in viewer PNG | 1.496 s | 1.366 s | 50.05 MiB | 50 |
| Licence illustration | 3.066 s | 2.923 s | 40.25 MiB | 138 |

PSM 6/11 comparisons on the first three examples took 0.42–1.76 seconds per run.
PSM 11 found additional field labels in the viewer; a bounded conservative PSM 11
fallback was added. None of these runs had a word score reaching the configured
98 threshold, so these examples require admin review. Word counts/checksum-valid
candidates are not accuracy against verified ground truth. No raw OCR or personal
fields were printed, persisted as benchmark output, or committed. Original PNGs
were not modified. These figures do not measure complete Go-worker/DB/API/bot
memory or demonstrate full-stack performance on the production 4-CPU/6-GB host.

## 18.9 Remaining operational decisions

- Identity-document retention duration, deletion/legal holds and backup expiry
  require an operator policy. Existing evidence is preserved; no new purge runs.
- Confirm jurisdiction/storage/consent requirements for IIN, documents and OCR
  with the operator's Kazakhstan data-protection policy; local processing does
  not itself establish all legal obligations.
- Manual B cannot prove destination service-area membership from text alone.
- Numeric-only legacy clients cannot truthfully receive coordinates for manual
  B. The write toggle stays off until all deployed readers/bots are upgraded.
- Calibrate recognition on a broader authorized set before relying on a claimed
  automatic approval rate. Automatic field matching does not verify forgery,
  liveness, document possession or vehicle-photo authenticity.

## Activation and safe fallback

1. Review/back up with the existing `make backup` procedure and verify the
   matching private files archive. Confirm storage/key custody and host headroom.
2. Build/deploy the API, all frontends and bots together using normal procedures;
   the existing migration service applies embedded migrations before API startup.
3. Enable `orders.manual_destination_enabled` in admin settings after readers
   upgrade. Pricing policy is `passenger_tariff_bounds`, using the active tariff.
4. Store a separate base64 32-byte `VERIFICATION_KEY` securely, enable
   `VERIFICATION_ENABLED`, and include the `ocr` Compose profile. Preserve this
   key with encrypted backups. Autoapproval follows `VERIFICATION_AUTO_APPROVE`.
5. Observe `/api/v1/admin/verification/queue`, notification lag and container/host
   resource metrics. Disable new feature writes/autoapproval/worker claims to
   fall back to existing manual review; retain schema, keys and history.

Production activation and destructive actions require the operator's explicit
deployment instruction. Do not run Down against feature data or invent missing
destination coordinates to fit an old reader.
