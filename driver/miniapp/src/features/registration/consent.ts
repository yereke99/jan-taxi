// Personal-data consent (Law of the Republic of Kazakhstan No. 94-V, Art. 7–8).
// The text ships with the Mini App (i18n key registration.consent.*); this
// version identifies it and is stored with the application on submit
// (driver_profiles.pd_consent_version). Change the version whenever the text
// changes in any language.

export const PD_CONSENT_VERSION = 'driver-pd-2026-10-v1'

/** Sections of the consent text, in display order (i18n keys under registration.consent.sections). */
export const CONSENT_SECTIONS = ['data', 'purpose', 'storage', 'access', 'retention', 'withdrawal'] as const
export type ConsentSection = (typeof CONSENT_SECTIONS)[number]
