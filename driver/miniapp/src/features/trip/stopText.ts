// Stop addresses as the driver reads them. The passenger may write an address
// in their own words (ARCHITECTURE §3, §5): a manual stop after A, which has
// no map point, or pickup A typed over its GPS or pin point (address_typed).
// That text is shown exactly as written and marked with manual.label
// («Жолаушы көрсеткен орын»); any other address goes through useAddressText,
// so a point without a name reads "Point on the map".

import type { StopSummary } from '../../api/types'
import { useAddressText } from '../../i18n/core'

/** What decides how a stop's address is shown; a missing address_typed reads false. */
export type StopAddress = Pick<StopSummary, 'mode' | 'address'> & { address_typed?: boolean }

/** True when the passenger wrote the address: a manual stop or a typed pickup A. */
export function isPassengerWritten(s: Pick<StopAddress, 'mode' | 'address_typed'> | null | undefined): boolean {
  return !!s && (s.mode === 'manual' || s.address_typed === true)
}

/** Display text of a stop: the passenger's words verbatim, any other address through useAddressText. */
export function useStopText(): (s: StopAddress | null | undefined) => string {
  const addr = useAddressText()
  return (s) => (s && isPassengerWritten(s) ? s.address : addr(s?.address))
}
