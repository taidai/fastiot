import { createSSRApp } from "vue";
import App from "./App.vue";

const currentPath = window.location.pathname.replace(/^\/uniapp/, "").replace(/\/index\.html$/, "").replace(/^\/+|\/+$/g, "") || "pages/login/index"
uni.reLaunch({ url: currentPath })

export function createApp() {
  const app = createSSRApp(App);
  return {
    app,
  };
}
