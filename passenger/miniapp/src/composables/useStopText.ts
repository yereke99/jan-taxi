// Stop addresses as the passenger reads them. The passenger may write an
// address in their own words (ARCHITECTURE §3, §5): a manual stop after A,
// which has no map point, or pickup A typed over its GPS or pin point
// (address_typed from the API, PlaceValue.typed while booking). That text is
// shown exactly as written; any other address goes through useAddressText, so
// a point without a name reads "Point on the map".

import type { StopSummary } from '../api/types'
import { isTypedPlace, type PlaceValue } from '../domain/route'
import { useAddressText } from '../i18n/core'

/** A stop from the API; a missing address_typed reads false. */
export type StopAddress = Pick<StopSummary, 'mode' | 'address'> & { address_typed?: boolean }

/** True when the passenger wrote the stop's address: a manual stop or a typed pickup A. */
export function isPassengerWritten(s: Pick<StopAddress, 'mode' | 'address_typed'> | null | undefined): boolean {
  return !!s && (s.mode === 'manual' || s.address_typed === true)
}

/** Display text of an API stop or a booking place: the passenger's words verbatim, any other address through useAddressText. */
export function useStopText(): (s: StopAddress | PlaceValue | null | undefined) => string {
  const addr = useAddressText()
  return (s) => {
    if (!s) return addr(null)
    const written = 'source' in s ? isTypedPlace(s) : isPassengerWritten(s)
    return written ? s.address : addr(s.address)
  }
}
