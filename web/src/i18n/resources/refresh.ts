import i18n from "../index";

const resources = {
  en: {
    refresh: {
      action: "Refresh",
      changed: "Data changed",
      loading: "Refreshing…",
      stale:
        "This snapshot may be out of date. Refresh to load current data and restart pagination.",
    },
  },
  zh: {
    refresh: {
      action: "刷新",
      changed: "数据已变化",
      loading: "正在刷新…",
      stale: "当前快照可能已失效，请刷新以加载最新数据并重新分页。",
    },
  },
};

for (const [language, resource] of Object.entries(resources)) {
  i18n.addResourceBundle(language, "translation", resource, true, true);
}
