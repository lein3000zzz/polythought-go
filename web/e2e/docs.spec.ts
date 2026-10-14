import { expect, test } from './fixtures'

test('the docs are served beside the app, searchable, under their own CSP', async ({ page }) => {
  const docs = await page.goto('/docs/')
  expect(docs?.status()).toBe(200)
  await expect(page.getByRole('heading', { name: 'Start here' })).toBeVisible()

  // The docs allow their own inline scripts by hash and WebAssembly for search; the app
  // keeps its stricter policy.
  const docsPolicy = docs?.headers()['content-security-policy'] ?? ''
  expect(docsPolicy).toContain("'wasm-unsafe-eval'")
  expect(docsPolicy).toMatch(/'sha256-[A-Za-z0-9+/=]+'/)
  expect(docsPolicy).not.toContain("'unsafe-inline' 'self'")
  expect(docsPolicy).toContain("frame-ancestors 'none'")

  const app = await page.request.get('/workflows')
  expect(app.headers()['content-security-policy']).toContain("script-src 'self';")

  // Guides and generated reference pages link up.
  await page.getByRole('link', { name: 'Concepts' }).first().click()
  await expect(page.getByRole('heading', { name: 'Concepts', level: 1 })).toBeVisible()
  await page.getByRole('link', { name: 'All nodes' }).click()
  await page.getByRole('link', { name: 'Webhook' }).first().click()
  await expect(page.getByText('output.webhook')).toBeVisible()

  // Search runs Pagefind's WebAssembly: the console fixture fails the test if the CSP
  // blocks it.
  await page.getByRole('button', { name: 'Search' }).first().click()
  await page.getByPlaceholder('Search').fill('trusted proxies')
  await page.locator('.pagefind-ui__result-link').first().click()
  await expect(page).toHaveURL(/\/docs\/(reference\/settings|self-hosting\/configuration)\//)

  // A missing page is a real 404, with the docs' own page.
  const missing = await page.goto('/docs/no-such-page/')
  expect(missing?.status()).toBe(404)
  await expect(page.getByText('There is no page here.', { exact: false })).toBeVisible()
})

test('the app links to the docs in a new tab', async ({ page, context }) => {
  await page.goto('/workflows')

  const [docs] = await Promise.all([
    context.waitForEvent('page'),
    page.getByRole('link', { name: 'Docs' }).click(),
  ])

  await expect(docs).toHaveURL(/\/docs\/$/)
  await expect(docs.getByRole('heading', { name: 'Start here' })).toBeVisible()
})
