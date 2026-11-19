/*
 * Trusted preview bridge. This file is served inside the sandboxed preview
 * frame, never in the platform page. It gives model markup only declarative
 * bindings: values arrive from the preview host, write requests leave through
 * it, and every problem is reported with a location.
 *
 * Keep this file free of line comments: it is compacted to one line before it
 * is embedded, so that the generated document keeps the model's line numbers.
 */
(function () {
  "use strict";

  if (window.parent === window) {
    return;
  }

  var token = "";
  var writeCounter = 0;
  var queuedErrors = [];
  var pendingWrites = {};
  var locateTimer = null;

  function post(message) {
    message.token = token;
    window.parent.postMessage(message, "*");
  }

  function markerOf(node) {
    return node && node.getAttribute ? node.getAttribute("data-fastiot-el") || "" : "";
  }

  function selectorOf(node) {
    var parts = [];
    var current = node;
    while (current && current.nodeType === 1 && current.tagName.toLowerCase() !== "html") {
      var part = current.tagName.toLowerCase();
      if (current.id) {
        part += "#" + current.id;
      } else if (current.parentNode) {
        var index = 1;
        var sibling = current;
        while ((sibling = sibling.previousElementSibling)) {
          if (sibling.tagName === current.tagName) {
            index++;
          }
        }
        part += ":nth-of-type(" + index + ")";
      }
      parts.unshift(part);
      current = current.parentElement;
    }
    return parts.join(" > ");
  }

  function format(node, value) {
    if (value === null || value === undefined) {
      return "—";
    }
    var kind = node.getAttribute("data-fastiot-format") || "";
    if (kind === "text") {
      return String(value);
    }
    if (kind === "bool") {
      var labels = (node.getAttribute("data-fastiot-labels") || "").split(",");
      if (labels.length === 2 && labels[0] !== "" && labels[1] !== "") {
        return value ? labels[0] : labels[1];
      }
      return value ? "1" : "0";
    }
    var number = Number(value);
    if (!isFinite(number)) {
      return String(value);
    }
    var decimals = Number(node.getAttribute("data-fastiot-decimals"));
    if (!isFinite(decimals) || decimals < 0 || decimals > 6) {
      decimals = 1;
    }
    return number.toFixed(decimals);
  }

  function applyValues(values) {
    var nodes = document.querySelectorAll("[data-fastiot-point]");
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      var id = node.getAttribute("data-fastiot-point");
      var entry = values && values[id];
      if (!entry) {
        continue;
      }
      node.textContent = format(node, entry.value);
      node.setAttribute("data-fastiot-quality", entry.quality || "good");
      if (entry.time) {
        node.setAttribute("data-fastiot-updated", String(entry.time));
      }
    }
    /* 3D screens read raw values from this event; DOM bindings stay declarative. */
    try {
      document.dispatchEvent(new CustomEvent("fastiot:values", { detail: { values: values || {} } }));
    } catch (error) {
      /* the screen simply does not observe values in old engines */
    }
  }

  function applyWriteResult(result) {
    var requestId = String(result.requestId || "");
    var node = pendingWrites[requestId];
    if (node) {
      node.setAttribute("data-fastiot-state", result.status === "simulated" ? "simulated" : "rejected");
      node.removeAttribute("data-fastiot-pending");
      delete pendingWrites[requestId];
    }
    try {
      document.dispatchEvent(new CustomEvent("fastiot:write-result", { detail: result }));
    } catch (error) {
      /* the screen simply does not observe write results in old engines */
    }
  }

  function onWrite(event) {
    var node = event.currentTarget;
    var binding = node.getAttribute("data-fastiot-write");
    var value = Number(node.getAttribute("data-fastiot-value"));
    if (!binding || !isFinite(value)) {
      return;
    }
    writeCounter += 1;
    var requestId = "w" + writeCounter;
    pendingWrites[requestId] = node;
    node.setAttribute("data-fastiot-state", "pending");
    post({ type: "fastiot:write", binding: binding, value: value, requestId: requestId, element: markerOf(node) });
  }

  function attach() {
    var buttons = document.querySelectorAll("[data-fastiot-write]");
    for (var i = 0; i < buttons.length; i++) {
      if (buttons[i].getAttribute("data-fastiot-bound") === "1") {
        continue;
      }
      buttons[i].setAttribute("data-fastiot-bound", "1");
      buttons[i].addEventListener("click", onWrite);
    }
    var ids = {};
    var nodes = document.querySelectorAll("[data-fastiot-point],[data-fastiot-write]");
    for (var j = 0; j < nodes.length; j++) {
      var read = nodes[j].getAttribute("data-fastiot-point");
      var write = nodes[j].getAttribute("data-fastiot-write");
      if (read) {
        ids[read] = true;
      }
      if (write) {
        ids[write] = true;
      }
    }
    return Object.keys(ids);
  }

  function layoutFindings() {
    var findings = [];
    var root = document.documentElement;
    if (!root) {
      return findings;
    }
    var width = root.clientWidth;
    if (root.scrollWidth - width > 2) {
      var widest = null;
      var widestRight = width;
      var candidates = document.body ? document.body.querySelectorAll("*") : [];
      var limit = Math.min(candidates.length, 2000);
      for (var i = 0; i < limit; i++) {
        var box = candidates[i].getBoundingClientRect();
        if (box.right > widestRight) {
          widest = candidates[i];
          widestRight = box.right;
        }
      }
      findings.push({
        code: "layout_overflow", origin: "layout", severity: "blocking",
        message: "画面宽度超出可视区域 " + Math.round(root.scrollWidth - width) + " 像素",
        element: widest ? markerOf(widest) : "",
        selector: widest ? selectorOf(widest) : "",
        detail: { page_width: root.scrollWidth, viewport_width: width }
      });
    }
    var nodes = document.querySelectorAll("[data-fastiot-point],[data-fastiot-write]");
    for (var j = 0; j < nodes.length; j++) {
      var node = nodes[j];
      var rect = node.getBoundingClientRect();
      var style = window.getComputedStyle(node);
      if (rect.width <= 0 || rect.height <= 0 || style.display === "none" || style.visibility === "hidden") {
        findings.push({
          code: "layout_invisible", origin: "layout", severity: "blocking",
          message: "绑定元素不可见，运行值不会显示在这个位置",
          element: markerOf(node), selector: selectorOf(node),
          detail: { width: rect.width, height: rect.height }
        });
        continue;
      }
      if (node.scrollWidth - node.clientWidth > 2 || node.scrollHeight - node.clientHeight > 2) {
        findings.push({
          code: "layout_clipped", origin: "layout", severity: "blocking",
          message: "绑定元素内容被裁切",
          element: markerOf(node), selector: selectorOf(node),
          detail: {
            content_width: node.scrollWidth, box_width: node.clientWidth,
            content_height: node.scrollHeight, box_height: node.clientHeight
          }
        });
      }
      if (rect.right < 0 || rect.left > width || rect.bottom < 0) {
        findings.push({
          code: "layout_offscreen", origin: "layout", severity: "warning",
          message: "绑定元素在可视区域之外",
          element: markerOf(node), selector: selectorOf(node),
          detail: { left: rect.left, right: rect.right, width: width }
        });
      }
    }
    return findings;
  }

  function recordError(message, line, column) {
    var finding = {
      code: "runtime_error", origin: "runtime", severity: "blocking",
      message: String(message || "脚本运行错误"),
      line: line || 0, column: column || 0
    };
    if (token) {
      post({ type: "fastiot:error", finding: finding });
    } else {
      queuedErrors.push(finding);
    }
  }

  function locate(marker) {
    if (!/^[A-Za-z0-9_-]{1,40}$/.test(marker || "")) {
      return;
    }
    var node = document.querySelector('[data-fastiot-el="' + marker + '"]');
    if (!node) {
      post({ type: "fastiot:locate-result", element: marker, found: false });
      return;
    }
    node.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
    var previous = node.style.outline;
    node.style.outline = "3px solid #f97316";
    node.style.outlineOffset = "2px";
    if (locateTimer) {
      window.clearTimeout(locateTimer);
    }
    locateTimer = window.setTimeout(function () {
      node.style.outline = previous;
      node.style.outlineOffset = "";
    }, 2200);
    post({ type: "fastiot:locate-result", element: marker, found: true });
  }

  function start() {
    var ids = attach();
    post({ type: "fastiot:ready", bindings: ids, findings: layoutFindings() });
    for (var i = 0; i < queuedErrors.length; i++) {
      post({ type: "fastiot:error", finding: queuedErrors[i] });
    }
    queuedErrors = [];
  }

  window.addEventListener("error", function (event) {
    recordError(event.message, event.lineno, event.colno);
  });
  window.addEventListener("unhandledrejection", function (event) {
    var reason = event.reason;
    recordError(reason && reason.message ? reason.message : String(reason), 0, 0);
  });

  window.addEventListener("message", function (event) {
    if (event.source !== window.parent) {
      return;
    }
    var data = event.data;
    if (!data || typeof data !== "object") {
      return;
    }
    if (data.type === "fastiot:init") {
      token = String(data.token || "");
      start();
      return;
    }
    if (!token || String(data.token || "") !== token) {
      return;
    }
    if (data.type === "fastiot:values") {
      applyValues(data.values || {});
    } else if (data.type === "fastiot:write-result") {
      applyWriteResult(data);
    } else if (data.type === "fastiot:check") {
      post({ type: "fastiot:findings", findings: layoutFindings() });
    } else if (data.type === "fastiot:locate") {
      locate(String(data.element || ""));
    }
  });
})();
