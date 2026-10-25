/** The project's repository, which every page links to (D368). */
export const repoURL = 'https://github.com/lein3000zzz/polythought-go'

/** The version of Polythought this page's app was built as: dev, unless a release built it (D425). */
export const appVersion: string = import.meta.env.VITE_POLYTHOUGHT_VERSION || 'dev'

/** A release's page, with its notes. */
export const releaseURL = (version: string) => `${repoURL}/releases/tag/v${version}`
