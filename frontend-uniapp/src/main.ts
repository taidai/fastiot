import { createSSRApp } from "vue";
import App from "./App.vue";

const hashPath = window.location.hash.replace(/^#\/?/, "")
const pathFromHistory = window.location.pathname.replace(/^\/uniapp/, "").replace(/\/index\.html$/, "").replace(/^\/+|\/+$/g, "")
const requestedPath = hashPath || pathFromHistory || "pages/login/index"
const currentPath = uni.getStorageSync("fastems-token") ? requestedPath : "pages/login/index"
// Normalize the entry before the H5 router mounts. An initial reLaunch races
// page onShow/onMounted authentication and can remount a partially typed login.
const entry = new URL(window.location.href)
entry.pathname = "/uniapp/"
entry.hash = `/${currentPath}`
window.history.replaceState(null, "", entry)

export function createApp() {
  const app = createSSRApp(App);
  return {
    app,
  };
}
