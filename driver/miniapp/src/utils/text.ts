// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Free-text rules mirror the backend (ARCHITECTURE §11): the API counts Unicode
// code points after NFC normalisation, so the UI must count the same way or a
// comment that "fits" here would be rejected there.

/** Maximum length of free-text fields (comment, cargo description, rating comment). */
export const TEXT_MAX = 120

/** Number of Unicode code points after NFC normalisation. */
export function codePointLength(s: string): number {
  return [...s.normalize('NFC')].length
}

// Bidi embedding/override/isolate controls and marks; they can visually reorder
// text in notifications and admin screens, so the backend strips them.
const cp = (...codes: number[]): string => String.fromCharCode(...codes)
const BIDI_CONTROLS = new RegExp(`[${cp(0x061c, 0x200e, 0x200f)}${cp(0x202a)}-${cp(0x202e)}${cp(0x2066)}-${cp(0x2069)}]`, 'g')
// C0/C1 control characters except the whitespace handled separately below.
const CONTROL_CHARS = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F-\u009F]/g
const LINE_BREAKS_AND_TABS = new RegExp(`[\r\n\t${cp(0x2028, 0x2029)}]`, 'g')
const SPACE_RUNS = / {2,}/g

/**
 * Normalises free text exactly like the backend's textutil.CleanText:
 * NFC, newlines/tabs → spaces, control and bidi characters removed,
 * runs of spaces collapsed, trimmed.
 */
export function cleanText(s: string): string {
  return s
    .normalize('NFC')
    .replace(LINE_BREAKS_AND_TABS, ' ')
    .replace(CONTROL_CHARS, '')
    .replace(BIDI_CONTROLS, '')
    .replace(SPACE_RUNS, ' ')
    .trim()
}

/** Length of the text as the backend will count it (after cleaning). */
export function cleanedLength(s: string): number {
  return codePointLength(cleanText(s))
}

/** True when the cleaned text is within `max` code points. */
export function fitsLimit(s: string, max: number = TEXT_MAX): boolean {
  return cleanedLength(s) <= max
}
