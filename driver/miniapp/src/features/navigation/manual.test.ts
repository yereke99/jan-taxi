import {describe,it,expect} from 'vitest'
import {navigationPrefix,planNavigation} from './twogis'
const a={lat:48.8264,lon:58.1442}
const via={lat:48.83,lon:58.15}
const b={mode:'manual',lat:null,lon:null}
describe('navigation with a manual final landmark',()=>{
 it.each(['ios','android'])('preserves navigation to real pickup on %s',platform=>{
  const p=planNavigation({base:'https://2gis.kz',platform,origin:null,remaining:navigationPrefix([a,b])})
  expect(p?.nextUrl).toContain('58.144200,48.826400')
  expect(p?.nextUrl).not.toContain('null')
 })
 it('retains known intermediate points and stops when only manual B remains',()=>{
  expect(navigationPrefix([a,via,b])).toEqual([a,via]);expect(navigationPrefix([b])).toEqual([])
  expect(planNavigation({base:'https://2gis.kz',platform:'android',origin:a,remaining:navigationPrefix([b])})).toBeNull()
 })
 it('never skips invalid intermediate points or manual points before the end',()=>{
  expect(navigationPrefix([a,{lat:null,lon:null},via,b])).toEqual([])
  expect(navigationPrefix([a,b,via])).toEqual([])
  expect(navigationPrefix([a,{...b,lat:48.9}])).toEqual([])
 })
})
