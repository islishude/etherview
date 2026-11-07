import i18n from "./index";
import { adminResources } from "./resources/admin";

// Administration copy is loaded with the management routes, not the public shell.
i18n.addResourceBundle("en", "translation", adminResources.en, true, true);
i18n.addResourceBundle("zh", "translation", adminResources.zh, true, true);
