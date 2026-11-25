// Mode is fixed for this document. Switching mode requires a full reload so
// preview state and in-flight live requests cannot cross the boundary.
export const PREVIEW_MODE = typeof location !== "undefined" && new URLSearchParams(location.search).get("preview") === "1"
