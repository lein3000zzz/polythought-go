// The little of big.js (vendored, v7.0.1) the SDK uses. Code sees the SDK's Decimal.
interface BigConstructor {
  (n: string | number | BigValue): BigValue
  new (n: string | number | BigValue): BigValue
  (): BigConstructor // a fresh constructor, with its own settings
  DP: number
  RM: number
  NE: number
  PE: number
}

// BigValue is what Decimal describes, in sdk.ts.
type BigValue = object

declare const Big: BigConstructor
export default Big
