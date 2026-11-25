// Temporary visual-acceptance switch. The preserved live pages and API client
// will be re-enabled together after the prototype frontend is accepted.
// Adding ?live=1 to a page URL keeps the real integration available for contract tests.
export const PREVIEW_MODE = !(typeof location !== "undefined" && /[?&]live=1(?:[&#]|$)/.test(location.href))
