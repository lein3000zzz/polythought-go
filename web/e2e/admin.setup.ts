import { test as setup } from '@playwright/test'

import { admin, adminState, signIn } from './fixtures'

// Signs the admin in once for every test: the server allows only a few sign-ins per
// address in a row, which a test signing in each time would use up.
setup('sign in as admin', async ({ page }) => {
  await signIn(page, admin)
  await page.context().storageState({ path: adminState })
})
