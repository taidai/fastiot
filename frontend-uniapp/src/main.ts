import { createSSRApp } from "vue";
import App from "./App.vue";

const hashPath = window.location.hash.replace(/^#\/?/, "")
const pathFromHistory = window.location.pathname.replace(/^\/uniapp/, "").replace(/\/index\.html$/, "").replace(/^\/+|\/+$/g, "")
const currentPath = hashPath || pathFromHistory || "pages/login/index"
uni.reLaunch({ url: currentPath })

export function createApp() {
  const app = createSSRApp(App);
  return {
    app,
  };
}
