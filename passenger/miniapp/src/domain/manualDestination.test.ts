import { describe, expect, it } from 'vitest'
import { createStops, setPlace, toStopInputs, isComplete, routeFingerprint, addStop, moveStop, type PlaceValue } from './route'
import { parseOrderSummary, parseOrder } from '../api/parse'
import { placesOfStops } from './places'
import { orderMarkers, nextStop } from './orderView'
import { normalizePublicConfig } from '../services/config'

const a: PlaceValue={lat:48.8264,lon:58.1442,address:'A',subtitle:null,source:'gps',inServiceArea:true}
const manual: PlaceValue={lat:null,lon:null,address:'Ескі мәдениет үйі',subtitle:null,source:'manual',inServiceArea:null}

describe('manual destinations',()=>{
 it('keeps A geolocated and serializes B as actual nulls',()=>{
  let s=createStops();s=setPlace(s,0,a);s=setPlace(s,1,manual)
  expect(isComplete(s)).toBe(true)
  expect(toStopInputs(s)).toEqual([{lat:a.lat,lon:a.lon,address:'A'},{mode:'manual',lat:null,lon:null,address:manual.address}])
  const invalid=setPlace(s,0,manual);expect(isComplete(invalid)).toBe(false)
  const old=routeFingerprint(s);s=setPlace(s,1,{...a,address:'B'});expect(routeFingerprint(s)).not.toBe(old)
  expect(toStopInputs(s)![1]).toEqual({lat:a.lat,lon:a.lon,address:'B'})
 })
 it('preserves the pickup and manual final B through intermediate editing',()=>{
  let s=createStops();s=setPlace(s,0,a);s=setPlace(s,1,manual);s=addStop(s,5);s=setPlace(s,1,{...a,lat:48.83,address:'via1'});s=addStop(s,5);s=setPlace(s,2,{...a,lat:48.84,address:'via2'})
  s=moveStop(s,1,2);expect(s[0]!.place).toEqual(a);expect(s[3]!.place).toEqual(manual);expect(isComplete(s)).toBe(true)
 })
 it('does not convert NULL coordinates or unknown metrics into zero in history',()=>{
  const o=parseOrderSummary({id:'id',status:'completed',pickup:{lat:a.lat,lon:a.lon,address:'A'},destination:{mode:'manual',lat:null,lon:null,address:manual.address},distance_m:null})!
  expect(o.destination.lat).toBeNull();expect(o.distance_m).toBeNull()
 })
 it('keeps manual text for reorder while omitting it from maps and navigation',()=>{
  const o=parseOrder({id:'id',status:'in_progress',stops:[{id:'a',kind:'pickup',seq:0,lat:a.lat,lon:a.lon,address:'A',completed_at:'now'},{id:'b',seq:1,kind:'destination',mode:'manual',lat:null,lon:null,address:manual.address,completed_at:null}],distance_m:null,duration_s:null,route:{geometry:null,distance_m:null,duration_s:null}})
  expect(o.stops[1]!.lat).toBeNull();expect(o.distance_m).toBeNull();expect(orderMarkers(o)).toHaveLength(1);expect(nextStop(o)).toBeNull()
  expect(placesOfStops(o.stops)[1]).toMatchObject({source:'manual',lat:null,lon:null,address:manual.address})
 })
 it('defaults new booking creation off and honors only explicit runtime enablement',()=>{
  expect(normalizePublicConfig(null).orders?.manual_destination_enabled).toBe(false)
  expect(normalizePublicConfig({orders:{manual_destination_enabled:true,manual_destination_pricing:'passenger_tariff_bounds'}}).orders).toEqual({manual_destination_enabled:true,manual_destination_pricing:'passenger_tariff_bounds'})
 })
})
