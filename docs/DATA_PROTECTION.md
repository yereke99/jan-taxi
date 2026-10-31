# Personal data protection

A practical checklist for operating Jan Taxi under the Law of the Republic of
Kazakhstan of 21 May 2013 No. 94-V "On personal data and their protection"
(adilet `Z1300000094`, consolidated text with amendments up to Law No. 326-VIII of
24 June 2026). It records what the software does and what the operator (the ИП or
ТОО running the service) must do.

This is engineering guidance, not legal advice. Article references were taken
from a consolidated mirror of the law; have a lawyer confirm them against the
official text on adilet.zan.kz before relying on them.

## 1. What personal data the system holds

| Who | Data | Where | Purpose |
|---|---|---|---|
| Passengers | Telegram id, first/last name, username, language; trip addresses and coordinates, comments; for parcels the recipient's phone number; ratings, disputes | PostgreSQL | taking and fulfilling orders, support, disputes |
| Drivers | everything above plus Telegram-verified phone, IIN (ЖСН), optional patronymic, avatar photo (face), make/model/colour/plate and vehicle class, location and location history, wallet ledger, top-ups, Kaspi payment receipts and transaction numbers, moderation history, consent timestamp and version; ID card photos (front and back) and vehicle photos only for drivers who uploaded them before 31 October 2026 | PostgreSQL, private `files` volume | registration, uniqueness checks (one live driver per IIN, phone and plate), dispatch, settlement of commission |
| Admins | email, name, password hash, sessions, IP addresses and actions in the audit log | PostgreSQL | access control, accountability |
| Everyone | IP addresses, user agents, request paths | container logs (rotated) | security, troubleshooting |
| Everyone | all of the above | `./backups` and off-site copies | recovery |

The IIN identifies a person on its own; ID card photos contain the IIN, full name
and a face image, which the law lists as identifying data. Treat the IIN and the
ID card photos kept from before 31 October 2026 as **restricted-access** data.
New registrations collect the IIN as typed text only, no document scans.

## 2. Localisation (Art. 12(2))

- [ ] The server, the database, the `files` volume and every backup copy are in
      data centres located in Kazakhstan (confirmed in writing by the provider).
- [ ] No logs, dumps or file archives are copied to services outside Kazakhstan
      (cloud drives, foreign log services, personal laptops abroad).
- [ ] Known flows that leave Kazakhstan are listed in the consent texts:
  - **Telegram**: bot messages (order details, names, statuses) and Mini App
    sign-in pass through Telegram's servers abroad. This is a cross-border
    transfer.
  - **Map tiles**: the user's device loads tiles from OpenFreeMap; the provider
    sees the device's IP address and the map area viewed.
  - **Public geocoder** (only if `GEOCODING_PROVIDER=photon` without a self-hosted
    `GEOCODING_BASE_URL`): address search text is sent by the server to
    photon.komoot.io (Germany), without user identifiers. Prefer the self-hosted
    Photon or a provider in Kazakhstan.
  - **2GIS / Kaspi Pay links**: opened by the user in the 2GIS or Kaspi app; the
    coordinates or payment details in the link go to those services.
- [ ] The OSRM router runs on the same server; route coordinates do not leave it.

## 3. Consent (Art. 7, Art. 8)

Consent may be given in writing, through a consent service, or "in another way
that allows confirming that consent was obtained". A logged confirmation in the
app with a timestamp and the version of the text is used for drivers.

The consent text must contain (Art. 8(4)):

- [ ] the operator's name and BIN (or IIN for an ИП);
- [ ] the subject's full name (taken from the profile);
- [ ] the period for which consent is given;
- [ ] whether data is transferred to third parties (Kaspi Pay, Telegram, 2GIS
      links, hosting provider);
- [ ] whether data is transferred across borders (Telegram, see section 2);
- [ ] whether data is published in publicly available sources (no);
- [ ] the list of data collected (section 1).

Status in the software:

- **Drivers**: the time and the version of the accepted consent text are stored
  in `driver_profiles.pd_consent_at` and `pd_consent_version` (migration 00004).
  Keep every published version of the text with its version identifier. The text
  shown with the single consent checkbox above «Тіркелуді аяқтау» (version
  `driver-pd-2026-10-v3`, 31 October 2026) lists the profile photo, names, IIN,
  Telegram phone, vehicle data and location; it no longer mentions ID scans,
  document numbers or automatic document comparison. A driver who accepted an
  older version accepts the new one when finishing a corrected registration.
- **Passengers**: the database has no consent record for passengers. The
  operator must decide how passengers are informed and consent (for example a
  notice and confirmation on first launch, recorded with a version) and request
  the corresponding change from the developers.
- [ ] Withdrawal of consent: processing must stop within **15 working days** of
      the request or a reasoned refusal must be given (Art. 8(7)). Consent cannot
      be withdrawn while it is needed to fulfil an outstanding obligation
      (Art. 8(2)), for example an unsettled top-up or an open dispute.
- [ ] Requests from data subjects (access, correction, withdrawal, deletion) are
      handled by the responsible person. There is no self-service deletion in the
      apps; changes are made by authorised admins and recorded in the audit log.

## 4. Retention and deletion

What the software deletes automatically:

| Data | Retention | Mechanism |
|---|---|---|
| Driver location history | 30 days | job `location_history_retention` |
| Processed Telegram update ids | 7 days | job `bot_update_cleanup` |
| Expired admin sessions | when expired | job `session_cleanup` |
| Files on disk without a database row (for example from an interrupted upload) | 24 hours | job `file_orphan_cleanup` (every 6 h) |
| Backups | `BACKUP_RETENTION_DAYS` (14 days) on the server; off-site copies by their own policy | `deploy/backup/backup.sh` |
| Container logs | until rotated (`LOG_MAX_SIZE` × `LOG_MAX_FILE` per container) | Docker |

What needs an operator decision:

- [ ] **ID card photos and vehicle photos** uploaded before 31 October 2026 (no
      new ones are collected): Art. 25 requires destroying personal data once the
      purpose is achieved. Decide how long they are kept. Files replaced or
      removed in the app are soft-deleted in the database but stay on disk; there
      is no purge job yet. Request one from the developers with the chosen period.
- [ ] **IIN**: needed while the driver profile is live (it keeps one driver per
      IIN). Decide how long it is kept after a driver is rejected or leaves; the
      value stays in the profile row until removed by request.
- [ ] Rows written by the retired document check (tables of migration 00011,
      encrypted with the operator's former verification key) are kept but no
      longer read by the software. Decide their retention together with the ID
      photos; destroying the key makes them unreadable.
- [ ] **Orders and status history**: the taxi rules (order No. 349, p. 402)
      require a dispatch service to keep records of orders and their hand-over to
      drivers, without a stated period. Set a period together with accounting and
      tax requirements.
- Finished **notification outbox** rows and attempts are pruned after 30 days by
  `notification_retention`; pending/sending messages are preserved for recovery.
- [ ] Deleted data persists in backups until they expire; state this in the
      privacy policy.

## 5. Access control and audit (restricted-access data)

Implemented:

- The IIN is a plain column, `driver_profiles.iin` (12 digits, migration 00013),
  unique among live drivers; it is not encrypted inside the database. The API
  returns it in full only to the driver it belongs to (their own registration);
  the driver's profile, the admin panel and application snapshots carry only
  `iin_masked` (`********1234`). It is never written to audit payloads, logs,
  bot messages, realtime events, bot offers or passenger payloads, and a
  duplicate is reported as `iin_taken` without naming the other driver.
- ID card photos (kept from before 31 October 2026) are stored only in the
  private `files` volume (mode 700, non-root owner), never served as static
  files, and served with `Cache-Control: private, no-store`. Images were
  re-encoded on upload with all EXIF metadata (including GPS) removed; ID PDFs
  accepted between 29 and 31 October 2026 are kept as uploaded.
- Only the roles `super_admin` and `driver_moderator` hold `documents.view`;
  every admin view of an ID document is written to the audit log
  (`admin_audit_logs`, visible to roles with `audit.view`).
- Passengers see only the assigned driver's photo, last-name initial and, while
  the order is active, the driver's phone. Drivers see the recipient's phone and
  the passenger's contact details only after accepting the order.
- Admin accounts: argon2id password hashes, 12+ character passwords, lockout
  after 5 failed sign-ins, session cookies limited to HTTPS and `/api`, CSRF
  protection, role checks on the server for every admin route.
- The database and internal API are not reachable from the internet; secrets are
  kept out of logs.

Operator checklist:

- [ ] Every admin has a personal account with the narrowest role; no shared
      accounts. Remove accounts of people who leave the same day.
- [ ] Review the audit log of ID-document views monthly.
- [ ] Server access only with personal SSH keys; the list of people with server
      or `docker` access is documented.
- [ ] Backup files are copied only over encrypted channels and stored encrypted.
      Database dumps contain every driver's IIN in clear: restrict who can read
      the database, `./backups` and the off-site copies to the named people, and
      never hand out a dump for debugging.

## 6. Operator duties (Art. 22, Art. 25, Art. 10-1, Art. 25-1)

- [ ] Appoint a person responsible for organising personal data processing
      (required for a legal entity, Art. 25(2)(10)) and publish their contacts.
- [ ] Approve the list of personal data processed (section 1) and a privacy
      policy; publish the policy and link it from both bots and the Mini Apps.
- [ ] Keep a register of transfers to third parties and across borders
      (Art. 22(1)(5)).
- [ ] Notification of the authorised body before processing (Art. 10-1, added by
      Law No. 326-VIII): small and medium operators are exempt. Under Art. 25-1 a
      small operator processes up to 10,000 subjects and a medium one up to
      500,000; processing restricted-access data (ID photos) moves the operator up
      one category. Re-check the category as the user base grows.

## 7. Breach response

Art. 25(2)(8): notify the authorised body for personal data protection **within
one working day from discovering** a security breach that affects personal data,
including the contact details of the responsible person.

1. **Contain**: revoke exposed credentials (bot tokens, `SESSION_SECRET`,
   `INTERNAL_BOT_SERVICE_SECRET`, database password, admin passwords; see
   [OPERATIONS.md](OPERATIONS.md#rotating-secrets)), disable compromised admin
   accounts, block the attack path (firewall, stop the affected service).
2. **Preserve evidence**: save container logs (`docker compose logs --no-color >
   incident-<date>.log`), the admin audit log for the period and a database dump;
   keep them in Kazakhstan with restricted access.
3. **Assess**: which data categories and how many people are affected, since when,
   whether ID photos or phone numbers were exposed.
4. **Notify** the authorised body within one working day of discovery: what
   happened, data categories and approximate number of subjects, measures taken,
   contacts of the responsible person. Notify affected users where there is a risk
   to them.
5. **Record** the incident, decisions and timeline; fix the cause; review this
   checklist.

Prepare the notification template and the authorised body's current contact
channel in advance; confirm which body it is at the time of launch.

## 8. Related obligations of a taxi dispatch service

Not personal-data rules, but they shape what data must be kept (Law "On automobile
transport", Art. 1, 26; order No. 349):

- A dispatch service run by an ИП or legal entity of Kazakhstan must work with
  carriers listed in the register of notifications (akimat) and check that
  platform drivers meet the legal requirements.
- The passenger must be told the car's plate number, the price and the pickup
  time; the apps show plate, fare and status.
- Records of orders and their hand-over to drivers must be kept (section 4).
