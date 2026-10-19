// Generates the vectors our order signing is checked against (D151), with Polymarket's
// official client, @polymarket/client, at the commit `make order-vectors` pins. It runs
// from the SDK's own source, packages/client/src, and prints JSON to stdout.
//
// Every value is fixed, salts and timestamps too, so the output only changes with the
// SDK. It first signs the vector Polymarket publishes (py-clob-client-v2 and
// rs-clob-client-v2 tests) and stops if that does not come out the same.

import { OrderSide } from '@polymarket/bindings';
import { SignatureType } from '@polymarket/bindings/clob';
import { WalletType } from '@polymarket/bindings/gamma';
import { TypedData } from 'ox';
import { privateKeyToAccount } from 'viem/accounts';

import {
  createExchangeOrderSignature,
  createExchangeOrderTypedDataPayload,
  type ExchangeOrderDomain,
  type ExchangeOrderInput,
  ExchangeOrderProtocolVersion,
} from './exchange';
import { computeLimitOrderAmounts, computeMarketOrderAmounts } from './actions/orders/amounts';
import { toScaledPrice } from './actions/orders/fixed';
import { isV2PositionId } from './protocol';
import { createApiKeyAuthTypedDataPayload } from './authentication';
import { production } from './environments';
import {
  deriveBeaconDepositWalletAddress,
  deriveProxyWalletAddress,
  deriveSafeWalletAddress,
  deriveUupsDepositWalletAddress,
  SignerType,
  wrapDepositWalletSignature,
} from './wallet';

// Anvil's first two keys: well known, never funded on Polygon.
const ownerKey = '0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80';
const sessionKey = '0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d';

const owner = privateKeyToAccount(ownerKey);
const session = privateKeyToAccount(sessionKey);

const proxyWallet = '0x2222222222222222222222222222222222222222';
const safeWallet = '0x3333333333333333333333333333333333333333';
const depositWallet = '0x1111111111111111111111111111111111111111';

const exchanges = {
  ctf: { address: '0xE111180000d2663C0091e4f400237545B87B996B', version: ExchangeOrderProtocolVersion.V2 },
  negRisk: { address: '0xe2222d279d744050d28e00520010520000310F59', version: ExchangeOrderProtocolVersion.V2 },
  v3: { address: '0xe3333700cA9d93003F00f0F71f8515005F6c00Aa', version: ExchangeOrderProtocolVersion.V3 },
} as const;

type Exchange = (typeof exchanges)[keyof typeof exchanges];

const zero = '0x0000000000000000000000000000000000000000000000000000000000000000';

async function sign(name: string, chainId: number, exchange: Exchange, order: ExchangeOrderInput, viaSession: boolean) {
  const domain: ExchangeOrderDomain = { chainId, exchange: exchange.address, protocolVersion: exchange.version };
  const payload = createExchangeOrderTypedDataPayload({ domain, order });
  const signer = viaSession ? session : owner;

  const raw = await signer.signTypedData(payload as never);
  let signature: string = createExchangeOrderSignature({ domain, order, signature: raw });

  if (viaSession) {
    signature = wrapDepositWalletSignature(
      { signer: session.address, signerType: SignerType.SESSION_KEY, wallet: depositWallet, walletType: WalletType.DEPOSIT_WALLET },
      signature as never,
    );
  }

  return {
    name,
    chainId,
    exchange: { address: exchange.address, version: exchange.version },
    session: viaSession,
    order: {
      salt: String(order.salt),
      maker: order.maker,
      signer: order.signer,
      tokenId: String(order.tokenId),
      makerAmount: String(order.makerAmount),
      takerAmount: String(order.takerAmount),
      side: order.side,
      signatureType: order.signatureType,
      timestamp: String(order.timestamp),
      metadata: order.metadata ?? zero,
      builder: order.builder ?? zero,
    },
    digest: TypedData.getSignPayload(payload as never),
    signature,
  };
}

function order(signatureType: SignatureType, side: OrderSide, over: Partial<ExchangeOrderInput> = {}): ExchangeOrderInput {
  const wallet = {
    [SignatureType.EOA]: { maker: owner.address, signer: owner.address },
    [SignatureType.POLY_PROXY]: { maker: proxyWallet, signer: owner.address },
    [SignatureType.POLY_GNOSIS_SAFE]: { maker: safeWallet, signer: owner.address },
    [SignatureType.POLY_1271]: { maker: depositWallet, signer: depositWallet },
  }[signatureType];

  return {
    salt: '479249096354',
    tokenId: '71321045679252212594626385532706912750332728571942532289631379312455583992563',
    makerAmount: side === OrderSide.BUY ? '5500000' : '10000000',
    takerAmount: side === OrderSide.BUY ? '10000000' : '5500000',
    side,
    signatureType,
    timestamp: '1760875200000',
    ...wallet,
    ...over,
  } as ExchangeOrderInput;
}

async function main() {
  // Polymarket's published vector first: if this differs, nothing below can be trusted.
  const published = await sign(
    'published: a Deposit Wallet buy on Amoy',
    80002,
    exchanges.ctf,
    {
      salt: '479249096354',
      maker: depositWallet,
      signer: depositWallet,
      tokenId: '1234',
      makerAmount: '100000000',
      takerAmount: '50000000',
      side: OrderSide.BUY,
      signatureType: SignatureType.POLY_1271,
      timestamp: '1710000000000',
    } as ExchangeOrderInput,
    false,
  );

  if (!published.signature.startsWith('0xa3a093c8') || !published.signature.endsWith('6c6465722900ba')) {
    throw new Error(`the published vector does not come out the same: ${published.signature}`);
  }

  const signatures = [published];
  const types = [
    ['an EOA', SignatureType.EOA],
    ['a proxy wallet', SignatureType.POLY_PROXY],
    ['a Safe', SignatureType.POLY_GNOSIS_SAFE],
    ['a Deposit Wallet', SignatureType.POLY_1271],
  ] as const;

  for (const [exchangeName, exchange] of Object.entries(exchanges)) {
    for (const [who, type] of types) {
      for (const side of [OrderSide.BUY, OrderSide.SELL]) {
        signatures.push(await sign(`${who}, a ${side.toLowerCase()}, on ${exchangeName}`, 137, exchange, order(type, side), false));
      }
    }

    signatures.push(
      await sign(`a session key for a Deposit Wallet, a buy, on ${exchangeName}`, 137, exchange, order(SignatureType.POLY_1271, OrderSide.BUY), true),
    );
  }

  signatures.push(
    await sign('an EOA with a builder code and metadata', 137, exchanges.ctf, order(SignatureType.EOA, OrderSide.BUY, {
      builder: '0x00000000000000000000000000000000000000000000000000000000000000b1',
      metadata: '0x00000000000000000000000000000000000000000000000000000000000000a1',
    }), false),
  );

  // Amounts, on the grid of each tick size: prices on the tick, sizes of two decimals.
  const ticks = ['0.1', '0.01', '0.005', '0.0025', '0.001', '0.0001'] as const;
  const prices: Record<(typeof ticks)[number], string[]> = {
    '0.1': ['0.1', '0.5', '0.9'],
    '0.01': ['0.01', '0.37', '0.99'],
    '0.005': ['0.005', '0.375', '0.995'],
    '0.0025': ['0.0025', '0.3775', '0.9975'],
    '0.001': ['0.001', '0.377', '0.999'],
    '0.0001': ['0.0001', '0.3777', '0.9999'],
  };
  const sizes = ['5', '12.34', '1000.01'];
  const dollars = ['1', '10', '12.34', '99.99'];

  const limitAmounts = [];
  const marketAmounts = [];

  for (const tick of ticks) {
    for (const price of prices[tick]) {
      for (const side of [OrderSide.BUY, OrderSide.SELL]) {
        for (const size of sizes) {
          const a = computeLimitOrderAmounts({ price: toScaledPrice(Number(price)), side, size: Number(size), tickSize: Number(tick) as never });
          limitAmounts.push({ tick, side, price, size, maker: String(a.offeredAmount), taker: String(a.requestedAmount) });
        }

        for (const protect of [false, true]) {
          for (const amount of side === OrderSide.BUY ? dollars : sizes) {
            const a = computeMarketOrderAmounts({
              amount: Number(amount),
              price: toScaledPrice(Number(price)),
              protectPrice: protect,
              side,
              tickSize: Number(tick) as never,
            });
            marketAmounts.push({ tick, side, amount, price, protect, maker: String(a.offeredAmount), taker: String(a.requestedAmount) });
          }
        }
      }
    }
  }

  // Which exchange: protocol v2 position IDs leave their reserved bits zero.
  const positionIds = [
    '1234',
    '71321045679252212594626385532706912750332728571942532289631379312455583992563',
    '52114319501245915516055106046884209969926127482827954674443846427813813222426',
    String((1n << 40n) - 1n),
    String(1n << 40n),
    String((1n << 104n) + 7n),
  ].map((tokenId) => ({ tokenId, v2: isV2PositionId(tokenId as never) }));

  // The ClobAuth signature API keys are made with (L1), for a few times and nonces.
  const clobAuth = [];
  for (const [timestamp, nonce] of [[1760875200, 0], [1760875261, 7]] as const) {
    const payload = createApiKeyAuthTypedDataPayload({ address: owner.address, chainId: 137, nonce, timestamp });
    clobAuth.push({ address: owner.address, chainId: 137, timestamp, nonce, signature: await owner.signTypedData(payload as never) });
  }

  // The wallets a key controls, as Polymarket derives them in production.
  const wallets = [owner.address, session.address, '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed'].map((signer) => ({
    signer,
    proxy: deriveProxyWalletAddress(signer as never, production.walletDerivation),
    safe: deriveSafeWalletAddress(signer as never, production.walletDerivation),
    depositBeacon: deriveBeaconDepositWalletAddress(signer as never, production.walletDerivation),
    depositUups: deriveUupsDepositWalletAddress(signer as never, production.walletDerivation),
  }));

  process.stdout.write(
    `${JSON.stringify(
      {
        sdk: { package: '@polymarket/client', source: 'https://github.com/Polymarket/ts-sdk', commit: process.env.SDK_COMMIT ?? 'unknown' },
        keys: { owner: ownerKey, session: sessionKey },
        signatures,
        limitAmounts,
        marketAmounts,
        positionIds,
        clobAuth,
        wallets,
      },
      null,
      2,
    )}\n`,
  );
}

await main();
