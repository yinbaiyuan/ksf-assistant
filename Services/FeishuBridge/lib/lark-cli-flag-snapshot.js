'use strict';

// Generated once from the pinned lark-cli 1.0.92 help surface.
// Do not regenerate on dependency upgrades without a capability/security review.
const LARK_CLI_FLAG_SNAPSHOT_VERSION = '1.0.92';

const LARK_CLI_FLAG_SNAPSHOT = Object.freeze({
  "apps +analytics-list": {
    "analytics": {
      "kind": "string"
    },
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "device-type": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "environment": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "granularity": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page": {
      "kind": "string"
    },
    "series": {
      "kind": "string"
    },
    "since": {
      "kind": "string"
    },
    "until": {
      "kind": "string"
    }
  },
  "apps +get": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "apps +list": {
    "app-type": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "keyword": {
      "kind": "string"
    },
    "ownership": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "apps +log-get": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "environment": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "log-id": {
      "kind": "string"
    }
  },
  "apps +log-list": {
    "api": {
      "kind": "string"
    },
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "environment": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "keyword": {
      "kind": "string"
    },
    "level": {
      "kind": "stringArray"
    },
    "max-duration": {
      "kind": "int"
    },
    "min-duration": {
      "kind": "int"
    },
    "module": {
      "kind": "string"
    },
    "page": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "since": {
      "kind": "string"
    },
    "trace-id": {
      "kind": "stringArray"
    },
    "until": {
      "kind": "string"
    },
    "user-id": {
      "kind": "string"
    }
  },
  "apps +metric-list": {
    "api": {
      "kind": "stringArray"
    },
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "down-sample": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "environment": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "metric": {
      "kind": "string"
    },
    "page": {
      "kind": "stringArray"
    },
    "series": {
      "kind": "string"
    },
    "since": {
      "kind": "string"
    },
    "until": {
      "kind": "string"
    }
  },
  "apps +release-get": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "release-id": {
      "kind": "string"
    }
  },
  "apps +release-list": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "status": {
      "kind": "string"
    }
  },
  "apps +session-get": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "session-id": {
      "kind": "string"
    }
  },
  "apps +session-list": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "apps +session-messages-list": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-token": {
      "kind": "string"
    },
    "session-id": {
      "kind": "string"
    },
    "turn-id": {
      "kind": "string"
    }
  },
  "apps +trace-get": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "environment": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "trace-id": {
      "kind": "string"
    }
  },
  "apps +trace-list": {
    "app-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "environment": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "root-span": {
      "kind": "string"
    },
    "since": {
      "kind": "string"
    },
    "trace-id": {
      "kind": "stringArray"
    },
    "until": {
      "kind": "string"
    },
    "user-id": {
      "kind": "string"
    }
  },
  "base +app-block-create": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "data-config": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "no-validate": {
      "kind": ""
    },
    "page-id": {
      "kind": "string"
    },
    "sub-type": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    }
  },
  "base +app-block-get": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "block-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-id": {
      "kind": "string"
    }
  },
  "base +app-block-get-data": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "block-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "base +app-block-list": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-id": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "base +app-block-update": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "block-id": {
      "kind": "string"
    },
    "data-config": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "no-validate": {
      "kind": ""
    },
    "page-id": {
      "kind": "string"
    }
  },
  "base +app-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "theme-style": {
      "kind": "string"
    },
    "workspace-token": {
      "kind": "string"
    }
  },
  "base +app-get": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "base +app-page-create": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    }
  },
  "base +app-page-get": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-id": {
      "kind": "string"
    }
  },
  "base +app-page-list": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "base +app-page-update": {
    "app-token": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "page-id": {
      "kind": "string"
    }
  },
  "base +base-block-create": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "parent-id": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    }
  },
  "base +base-block-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "parent-id": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    }
  },
  "base +base-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "fields": {
      "kind": "string"
    },
    "folder-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "table-name": {
      "kind": "string"
    },
    "time-zone": {
      "kind": "string"
    }
  },
  "base +base-get": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "base +dashboard-create": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "theme-style": {
      "kind": "string"
    }
  },
  "base +dashboard-get": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dashboard-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "base +dashboard-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "base +dashboard-update": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dashboard-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "theme-style": {
      "kind": "string"
    }
  },
  "base +data-query": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "dsl": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "base +field-get": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "field-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +field-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "limit": {
      "kind": "int"
    },
    "offset": {
      "kind": "int"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +field-update": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "field-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    },
    "yes": {
      "kind": ""
    }
  },
  "base +form-create": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "description": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +form-get": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "form-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +form-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +form-update": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "description": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "form-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +record-download-attachment": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file-token": {
      "kind": "stringArray"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    },
    "record-id": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +record-history-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "max-version": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "record-id": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +record-upload-attachment": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "field-id": {
      "kind": "string"
    },
    "file": {
      "kind": "stringArray",
      "fileInput": true
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "record-id": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +table-create": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "fields": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "view": {
      "kind": "string"
    }
  },
  "base +table-get": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +table-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "limit": {
      "kind": "int"
    },
    "offset": {
      "kind": "int"
    }
  },
  "base +table-update": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +template-categories": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "base +template-list": {
    "as": {
      "kind": "string"
    },
    "category-key": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "limit": {
      "kind": "int"
    },
    "offset": {
      "kind": "string"
    }
  },
  "base +template-search": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "keyword": {
      "kind": "string"
    },
    "limit": {
      "kind": "int"
    },
    "offset": {
      "kind": "string"
    }
  },
  "base +view-create": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +view-get": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "table-id": {
      "kind": "string"
    },
    "view-id": {
      "kind": "string"
    }
  },
  "base +view-list": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "limit": {
      "kind": "int"
    },
    "offset": {
      "kind": "int"
    },
    "table-id": {
      "kind": "string"
    }
  },
  "base +view-rename": {
    "as": {
      "kind": "string"
    },
    "base-token": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "table-id": {
      "kind": "string"
    },
    "view-id": {
      "kind": "string"
    }
  },
  "base +workspace-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    }
  },
  "base +workspace-entity-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "workspace-token": {
      "kind": "string"
    }
  },
  "calendar +join-event": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    }
  },
  "calendar +meeting": {
    "as": {
      "kind": "string"
    },
    "calendar-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "event-ids": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "calendar +room-find": {
    "as": {
      "kind": "string"
    },
    "attendee-ids": {
      "kind": "string"
    },
    "building": {
      "kind": "string"
    },
    "city": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "event-rrule": {
      "kind": "string"
    },
    "floor": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "max-capacity": {
      "kind": "int"
    },
    "min-capacity": {
      "kind": "int"
    },
    "room-name": {
      "kind": "string"
    },
    "slot": {
      "kind": "stringArray"
    },
    "timezone": {
      "kind": "string"
    }
  },
  "calendar +suggestion": {
    "as": {
      "kind": "string"
    },
    "attendee-ids": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "duration-minutes": {
      "kind": "int"
    },
    "end": {
      "kind": "string"
    },
    "event-rrule": {
      "kind": "string"
    },
    "exclude": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "start": {
      "kind": "string"
    },
    "timezone": {
      "kind": "string"
    }
  },
  "calendar calendars create": {
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "calendar calendars list": {
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "sync-token": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "calendar calendars patch": {
    "calendar-id": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "calendar calendars search": {
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "contact +get-user": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "user-id": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    }
  },
  "contact +search-bot": {
    "as": {
      "kind": "string"
    },
    "chat-ids": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "has-chatted": {
      "kind": ""
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "queries": {
      "kind": "string"
    },
    "query": {
      "kind": "string"
    }
  },
  "contact +search-user": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "exclude-external-users": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "has-chatted": {
      "kind": ""
    },
    "has-enterprise-email": {
      "kind": ""
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "lang": {
      "kind": "string"
    },
    "left-organization": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "queries": {
      "kind": "string"
    },
    "query": {
      "kind": "string"
    },
    "user-ids": {
      "kind": "string"
    }
  },
  "contact user_profiles batch_query": {
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "docs +history-list": {
    "as": {
      "kind": "string"
    },
    "doc": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "docs +history-revert": {
    "as": {
      "kind": "string"
    },
    "doc": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "history-version-id": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "wait-timeout-ms": {
      "kind": "int"
    }
  },
  "docs +media-download": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    }
  },
  "docs +media-insert": {
    "align": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "caption": {
      "kind": "string"
    },
    "doc": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string",
      "fileInput": true
    },
    "file-view": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "from-clipboard": {
      "kind": ""
    },
    "height": {
      "kind": "int"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "type": {
      "kind": "string"
    },
    "width": {
      "kind": "int"
    }
  },
  "docs +media-preview": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    }
  },
  "docs +media-upload": {
    "as": {
      "kind": "string"
    },
    "doc-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string",
      "fileInput": true
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "parent-node": {
      "kind": "string"
    },
    "parent-type": {
      "kind": "string"
    }
  },
  "docs +resource-download": {
    "as": {
      "kind": "string"
    },
    "doc": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    },
    "type": {
      "kind": "string"
    }
  },
  "docs +resource-update": {
    "as": {
      "kind": "string"
    },
    "doc": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "from-clipboard": {
      "kind": ""
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "offset-ratio-x": {
      "kind": "float"
    },
    "offset-ratio-y": {
      "kind": "float"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "docs +script": {
    "as": {
      "kind": "string"
    },
    "command": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "doc": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "presentation-decision": {
      "kind": "string",
      "fileInput": true
    }
  },
  "docs +update": {
    "as": {
      "kind": "string"
    },
    "block-id": {
      "kind": "string"
    },
    "command": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "doc": {
      "kind": "string"
    },
    "doc-format": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "end-block-id": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "pattern": {
      "kind": "string"
    },
    "reference-map": {
      "kind": "reference_map",
      "fileInput": true
    },
    "revision-id": {
      "kind": "int"
    },
    "src-block-ids": {
      "kind": "string"
    },
    "start-block-id": {
      "kind": "string"
    }
  },
  "drive +add-reply": {
    "as": {
      "kind": "string"
    },
    "comment-id": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "drive +batch-query-comments": {
    "as": {
      "kind": "string"
    },
    "comment-ids": {
      "kind": "strings"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "need-reaction": {
      "kind": ""
    },
    "need-relation": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "drive +list-replies": {
    "as": {
      "kind": "string"
    },
    "comment-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "need-reaction": {
      "kind": ""
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "drive +react-reply": {
    "action": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "emoji": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "reply-id": {
      "kind": "string"
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "drive +resolve-comment": {
    "as": {
      "kind": "string"
    },
    "comment-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "drive +restore-comment": {
    "as": {
      "kind": "string"
    },
    "comment-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "drive +update-reply": {
    "as": {
      "kind": "string"
    },
    "comment-id": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "reply-id": {
      "kind": "string"
    },
    "token": {
      "kind": "string"
    },
    "type": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "im +chat-create": {
    "as": {
      "kind": "string"
    },
    "bots": {
      "kind": "string"
    },
    "chat-mode": {
      "kind": "string"
    },
    "description": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "owner": {
      "kind": "string"
    },
    "set-bot-manager": {
      "kind": ""
    },
    "type": {
      "kind": "string"
    },
    "users": {
      "kind": "string"
    }
  },
  "im +chat-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "exclude-muted": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "sort": {
      "kind": "string"
    },
    "types": {
      "kind": "strings"
    },
    "user-id-type": {
      "kind": "string"
    }
  },
  "im +chat-members-list": {
    "as": {
      "kind": "string"
    },
    "chat-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "member-id-type": {
      "kind": "string"
    },
    "member-types": {
      "kind": "strings"
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "im +chat-search": {
    "as": {
      "kind": "string"
    },
    "chat-modes": {
      "kind": "string"
    },
    "disable-search-by-user": {
      "kind": ""
    },
    "dry-run": {
      "kind": ""
    },
    "exclude-muted": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "is-manager": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "member-ids": {
      "kind": "string"
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "query": {
      "kind": "string"
    },
    "search-types": {
      "kind": "string"
    },
    "sort": {
      "kind": "string"
    }
  },
  "im +chat-update": {
    "as": {
      "kind": "string"
    },
    "chat-id": {
      "kind": "string"
    },
    "description": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    }
  },
  "im +feed-group-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "end-time": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "start-time": {
      "kind": "string"
    }
  },
  "im +feed-group-list-item": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "end-time": {
      "kind": "string"
    },
    "feed-group-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "start-time": {
      "kind": "string"
    }
  },
  "im +feed-group-query-item": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "feed-group-id": {
      "kind": "string"
    },
    "feed-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    }
  },
  "im +feed-shortcut-create": {
    "as": {
      "kind": "string"
    },
    "chat-id": {
      "kind": "strings"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "head": {
      "kind": ""
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "tail": {
      "kind": ""
    }
  },
  "im +feed-shortcut-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "no-detail": {
      "kind": ""
    },
    "page-token": {
      "kind": "string"
    }
  },
  "im +flag-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-type": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "item-type": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "message-id": {
      "kind": "string"
    }
  },
  "im +flag-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "enrich-feed-thread": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  },
  "im +message-read-users": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "message-id": {
      "kind": "string"
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    }
  },
  "im +messages-edit": {
    "as": {
      "kind": "string"
    },
    "clear-attachments": {
      "kind": ""
    },
    "content": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "markdown": {
      "kind": "string"
    },
    "message-id": {
      "kind": "string"
    },
    "msg-type": {
      "kind": "string"
    },
    "set-attachments": {
      "kind": "strings"
    },
    "text": {
      "kind": "string"
    }
  },
  "im +messages-mget": {
    "as": {
      "kind": "string"
    },
    "download-resources": {
      "kind": ""
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "message-ids": {
      "kind": "string"
    },
    "no-reactions": {
      "kind": ""
    }
  },
  "im +messages-read-status": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "message-ids": {
      "kind": "string"
    }
  },
  "im +messages-reply": {
    "as": {
      "kind": "string"
    },
    "attachment": {
      "kind": "strings"
    },
    "audio": {
      "kind": "string"
    },
    "content": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "idempotency-key": {
      "kind": "string"
    },
    "image": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "markdown": {
      "kind": "string"
    },
    "message-id": {
      "kind": "string"
    },
    "msg-type": {
      "kind": "string"
    },
    "reply-in-thread": {
      "kind": ""
    },
    "text": {
      "kind": "string"
    },
    "video": {
      "kind": "string"
    },
    "video-cover": {
      "kind": "string"
    }
  },
  "im messages forward": {
    "message-id": {
      "kind": "string"
    },
    "receive-id-type": {
      "kind": "string"
    },
    "uuid": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im messages merge_forward": {
    "receive-id-type": {
      "kind": "string"
    },
    "uuid": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im messages urgent_app": {
    "message-id": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im pins create": {
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im pins list": {
    "chat-id": {
      "kind": "string"
    },
    "end-time": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "start-time": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im reactions create": {
    "message-id": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im reactions list": {
    "message-id": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "reaction-type": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "im threads forward": {
    "receive-id-type": {
      "kind": "string"
    },
    "thread-id": {
      "kind": "string"
    },
    "uuid": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "markdown +create": {
    "as": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string"
    },
    "folder-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "wiki-token": {
      "kind": "string"
    }
  },
  "markdown +diff": {
    "as": {
      "kind": "string"
    },
    "context-lines": {
      "kind": "int"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string"
    },
    "file-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "from-version": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "to-version": {
      "kind": "string"
    }
  },
  "markdown +fetch": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    }
  },
  "markdown +overwrite": {
    "as": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string"
    },
    "file-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    }
  },
  "markdown +patch": {
    "as": {
      "kind": "string"
    },
    "content": {
      "kind": "string",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "file-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "pattern": {
      "kind": "string",
      "fileInput": true
    },
    "regex": {
      "kind": ""
    }
  },
  "mindnotes nodes create": {
    "mindnote-id": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "mindnotes nodes list": {
    "mindnote-id": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "minutes +detail": {
    "as": {
      "kind": "string"
    },
    "chapter": {
      "kind": ""
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "keyword": {
      "kind": ""
    },
    "minute-tokens": {
      "kind": "string"
    },
    "output-dir": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    },
    "summary": {
      "kind": ""
    },
    "todo": {
      "kind": ""
    },
    "transcript": {
      "kind": ""
    }
  },
  "sheets +cells-merge": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "merge-type": {
      "kind": "+cells-merge"
    },
    "range": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cells-replace": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "find": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "include-formulas": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "match-case": {
      "kind": ""
    },
    "match-entire-cell": {
      "kind": ""
    },
    "range": {
      "kind": "string"
    },
    "regex": {
      "kind": "string"
    },
    "replacement": {
      "kind": "\"\""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cells-set-image": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "image": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    },
    "range": {
      "kind": "A1"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cells-set-style": {
    "as": {
      "kind": "string"
    },
    "background-color": {
      "kind": "#ffffff"
    },
    "border-styles": {
      "kind": "{ top: {style,weight,color}, bottom: ..., left: ..., right: ... }",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "font-color": {
      "kind": "#000000"
    },
    "font-family": {
      "kind": "Arial"
    },
    "font-line": {
      "kind": "string"
    },
    "font-size": {
      "kind": "float"
    },
    "font-style": {
      "kind": "string"
    },
    "font-weight": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "horizontal-alignment": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "number-format": {
      "kind": "@"
    },
    "print-schema": {
      "kind": ""
    },
    "range": {
      "kind": "A1:B2"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "vertical-alignment": {
      "kind": "string"
    },
    "word-wrap": {
      "kind": "string"
    }
  },
  "sheets +cells-unmerge": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +chart-config-update": {
    "as": {
      "kind": "string"
    },
    "chart-id": {
      "kind": "string"
    },
    "color-palette": {
      "kind": "string"
    },
    "colors": {
      "kind": "strings"
    },
    "data-label-position": {
      "kind": "string"
    },
    "data-labels": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "last-point-label": {
      "kind": ""
    },
    "legend-position": {
      "kind": "string"
    },
    "secondary-y-axis-title": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "smooth": {
      "kind": ""
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "stack": {
      "kind": "string"
    },
    "subtitle": {
      "kind": "string"
    },
    "title": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "x-axis-label-angle": {
      "kind": "int"
    },
    "x-axis-max": {
      "kind": "float"
    },
    "x-axis-min": {
      "kind": "float"
    },
    "x-axis-title": {
      "kind": "string"
    },
    "y-axis-label-angle": {
      "kind": "int"
    },
    "y-axis-max": {
      "kind": "float"
    },
    "y-axis-min": {
      "kind": "float"
    },
    "y-axis-title": {
      "kind": "string"
    }
  },
  "sheets +chart-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-example": {
      "kind": "string"
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "position",
      "fileInput": true
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +chart-data-update": {
    "as": {
      "kind": "string"
    },
    "chart-id": {
      "kind": "string"
    },
    "data-direction": {
      "kind": "string"
    },
    "data-range": {
      "kind": "string"
    },
    "dim1-index": {
      "kind": "int"
    },
    "dim2-indexes": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "group-index": {
      "kind": "int"
    },
    "header-range": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "key-index": {
      "kind": "int"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "size-index": {
      "kind": "int"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "x-index": {
      "kind": "int"
    },
    "y-index": {
      "kind": "int"
    }
  },
  "sheets +chart-list": {
    "as": {
      "kind": "string"
    },
    "chart-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +chart-update": {
    "as": {
      "kind": "string"
    },
    "chart-id": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "string",
      "fileInput": true
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cols-resize": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "range": {
      "kind": "A:E"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "type": {
      "kind": "pixel"
    },
    "url": {
      "kind": "string"
    },
    "width": {
      "kind": "string"
    },
    "widths": {
      "kind": "\"A\"",
      "fileInput": true
    }
  },
  "sheets +cond-format-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "style",
      "fileInput": true
    },
    "ranges": {
      "kind": "[\"A1:A100\",\"C2:C50\"]",
      "fileInput": true
    },
    "rule-type": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cond-format-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "rule-id": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cond-format-result-get": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "max-chars": {
      "kind": "int"
    },
    "range": {
      "kind": "A1:F10"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +cond-format-update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "+cond-format-create --properties",
      "fileInput": true
    },
    "ranges": {
      "kind": "[\"A1:A100\",\"C2:C50\"]",
      "fileInput": true
    },
    "rule-id": {
      "kind": "string"
    },
    "rule-type": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-freeze": {
    "as": {
      "kind": "string"
    },
    "cols": {
      "kind": "int"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "rows": {
      "kind": "int"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-group": {
    "as": {
      "kind": "string"
    },
    "depth": {
      "kind": "int"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "group-state": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "3:7"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-hide": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "3:7"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-insert": {
    "as": {
      "kind": "string"
    },
    "count": {
      "kind": "int"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "inherit-style": {
      "kind": "before"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "position": {
      "kind": "3"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-move": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "source-range": {
      "kind": "3:7"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "target": {
      "kind": "12"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-ungroup": {
    "as": {
      "kind": "string"
    },
    "depth": {
      "kind": "int"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "3:7"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dim-unhide": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "3:7"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dropdown-get": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "A2:A100"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dropdown-set": {
    "as": {
      "kind": "string"
    },
    "colors": {
      "kind": "[\"#1FB6C1\",\"#F006C2\"]",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "highlight": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "multiple": {
      "kind": "false"
    },
    "options": {
      "kind": "[\"opt1\",\"opt2\"]",
      "fileInput": true
    },
    "print-schema": {
      "kind": ""
    },
    "range": {
      "kind": "A2:A100"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "source-range": {
      "kind": "'Sheet1'!T1:T3"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +dropdown-update": {
    "as": {
      "kind": "string"
    },
    "colors": {
      "kind": "[\"#1FB6C1\",\"#F006C2\"]",
      "fileInput": true
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "highlight": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "multiple": {
      "kind": ""
    },
    "options": {
      "kind": "[\"opt1\",\"opt2\"]",
      "fileInput": true
    },
    "print-schema": {
      "kind": ""
    },
    "ranges": {
      "kind": "[\"Sheet1!A2:A100\",\"Sheet1!C2:C100\"]",
      "fileInput": true
    },
    "source-range": {
      "kind": "'Sheet1'!T1:T3"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +filter-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "rules",
      "fileInput": true
    },
    "range": {
      "kind": "A1:F1000"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +filter-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +filter-update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "rules",
      "fileInput": true
    },
    "range": {
      "kind": "A1:F1000"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +filter-view-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "rules?",
      "fileInput": true
    },
    "range": {
      "kind": "A1:F1000"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "view-name": {
      "kind": "string"
    }
  },
  "sheets +filter-view-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "view-id": {
      "kind": "string"
    }
  },
  "sheets +filter-view-update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "rules?",
      "fileInput": true
    },
    "range": {
      "kind": "A1:F1000"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "view-id": {
      "kind": "string"
    },
    "view-name": {
      "kind": "string"
    }
  },
  "sheets +float-image-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "image": {
      "kind": "string"
    },
    "image-name": {
      "kind": "logo.png"
    },
    "image-token": {
      "kind": "string"
    },
    "image-uri": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "offset-col": {
      "kind": "string"
    },
    "offset-row": {
      "kind": "string"
    },
    "position-col": {
      "kind": "A"
    },
    "position-row": {
      "kind": "int"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "size-height": {
      "kind": "int"
    },
    "size-width": {
      "kind": "int"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "z-index": {
      "kind": "int"
    }
  },
  "sheets +float-image-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "float-image-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +float-image-update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "float-image-id": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "image-name": {
      "kind": "logo.png"
    },
    "image-token": {
      "kind": "string"
    },
    "image-uri": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "offset-col": {
      "kind": "string"
    },
    "offset-row": {
      "kind": "string"
    },
    "position-col": {
      "kind": "A"
    },
    "position-row": {
      "kind": "int"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "size-height": {
      "kind": "int"
    },
    "size-width": {
      "kind": "int"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    },
    "z-index": {
      "kind": "int"
    }
  },
  "sheets +history-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "end-version": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +pivot-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "string",
      "fileInput": true
    },
    "range": {
      "kind": "F1"
    },
    "source": {
      "kind": "'SheetName'!StartCell:EndCell"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "target-position": {
      "kind": "A1"
    },
    "target-sheet-id": {
      "kind": "string"
    },
    "target-sheet-name": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +pivot-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "pivot-table-id": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +pivot-update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "pivot-table-id": {
      "kind": "string"
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "+pivot-list --pivot-table-id <id>",
      "fileInput": true
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +range-copy": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "paste-type": {
      "kind": "+range-copy"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "source-range": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "target-range": {
      "kind": "string"
    },
    "target-sheet-id": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +range-fill": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "series-type": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "source-range": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "target-range": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +range-move": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "source-range": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "target-range": {
      "kind": "string"
    },
    "target-sheet-id": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +range-sort": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "has-header": {
      "kind": "false"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "range": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "sort-keys": {
      "kind": "[{\"column\":\"<col letter>\",\"ascending\":<bool>}, ...]",
      "fileInput": true
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +rows-resize": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "height": {
      "kind": "string"
    },
    "heights": {
      "kind": "\"1\"",
      "fileInput": true
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "range": {
      "kind": "2:10"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "type": {
      "kind": "pixel"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sheet-copy": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "index": {
      "kind": "int"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "title": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sheet-hide": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sheet-info": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "include": {
      "kind": "strings"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "range": {
      "kind": "string"
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sheet-move": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "index": {
      "kind": "int"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "source-index": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sheet-rename": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "title": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sheet-unhide": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sparkline-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "{config (shared style), sparklines (array of mini-charts)}",
      "fileInput": true
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sparkline-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "group-id": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +sparkline-update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "group-id": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "properties": {
      "kind": "{config, sparklines}",
      "fileInput": true
    },
    "sheet-id": {
      "kind": "string"
    },
    "sheet-name": {
      "kind": "string"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +workbook-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "flag-name": {
      "kind": "string"
    },
    "folder-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "print-schema": {
      "kind": ""
    },
    "sheets": {
      "kind": "+table-put",
      "fileInput": true
    },
    "styles": {
      "kind": "{styles:[...]}",
      "fileInput": true
    },
    "title": {
      "kind": "string"
    },
    "values": {
      "kind": "[[\"alice\",95]]",
      "fileInput": true
    }
  },
  "sheets +workbook-export": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file-extension": {
      "kind": "csv"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output-path": {
      "kind": "./out.xlsx"
    },
    "sheet-id": {
      "kind": "+workbook-export"
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "sheets +workbook-import": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string",
      "fileInput": true
    },
    "folder-token": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    }
  },
  "sheets +workbook-info": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "spreadsheet-token": {
      "kind": "string"
    },
    "url": {
      "kind": "string"
    }
  },
  "task +comment": {
    "as": {
      "kind": "string"
    },
    "content": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "task-id": {
      "kind": "string"
    }
  },
  "task +followers": {
    "add": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "idempotency-key": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "remove": {
      "kind": "string"
    },
    "task-id": {
      "kind": "string"
    }
  },
  "task +set-ancestor": {
    "ancestor-id": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "task-id": {
      "kind": "string"
    }
  },
  "task +tasklist-create": {
    "as": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "member": {
      "kind": "string"
    },
    "name": {
      "kind": "string"
    }
  },
  "task +tasklist-task-add": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "section-guid": {
      "kind": "string"
    },
    "task-id": {
      "kind": "string"
    },
    "tasklist-id": {
      "kind": "string"
    }
  },
  "task +upload-attachment": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "file": {
      "kind": "string",
      "fileInput": true
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "resource-id": {
      "kind": "string"
    },
    "resource-type": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    }
  },
  "task custom_fields create": {
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task custom_fields get": {
    "custom-field-guid": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task custom_fields list": {
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "resource-id": {
      "kind": "string"
    },
    "resource-type": {
      "kind": "string"
    },
    "update-msec": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task custom_fields patch": {
    "custom-field-guid": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task sections create": {
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task sections list": {
    "resource-type": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "resource-id": {
      "kind": "string"
    },
    "update-msec": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task sections patch": {
    "section-guid": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task subtasks create": {
    "task-guid": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "data": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task subtasks list": {
    "task-guid": {
      "kind": "string"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-delay": {
      "kind": "int"
    },
    "page-limit": {
      "kind": "int"
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "task tasklists get": {
    "tasklist-guid": {
      "kind": "string"
    },
    "user-id-type": {
      "kind": "string"
    },
    "params": {
      "kind": "string"
    },
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "output": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "json": {
      "kind": ""
    }
  },
  "vc +meeting-events": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "end": {
      "kind": "string"
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "meeting-id": {
      "kind": "string"
    },
    "page-all": {
      "kind": ""
    },
    "page-size": {
      "kind": "string"
    },
    "page-token": {
      "kind": "string"
    },
    "start": {
      "kind": "string"
    }
  },
  "whiteboard +export": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "output": {
      "kind": "string"
    },
    "output-type": {
      "kind": "string"
    },
    "overwrite": {
      "kind": ""
    },
    "whiteboard-token": {
      "kind": "string"
    }
  },
  "whiteboard +update": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "idempotent-token": {
      "kind": "string"
    },
    "input_format": {
      "kind": "string"
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "overwrite": {
      "kind": ""
    },
    "source": {
      "kind": "string",
      "fileInput": true
    },
    "whiteboard-token": {
      "kind": "string"
    }
  },
  "wiki +member-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "space-id": {
      "kind": "string"
    }
  },
  "wiki +node-copy": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "node-token": {
      "kind": "string"
    },
    "space-id": {
      "kind": "string"
    },
    "target-parent-node-token": {
      "kind": "string"
    },
    "target-space-id": {
      "kind": "string"
    },
    "title": {
      "kind": "string"
    },
    "yes": {
      "kind": ""
    }
  },
  "wiki +node-create": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "node-type": {
      "kind": "string"
    },
    "obj-type": {
      "kind": "string"
    },
    "origin-node-token": {
      "kind": "string"
    },
    "parent-node-token": {
      "kind": "string"
    },
    "space-id": {
      "kind": "string"
    },
    "title": {
      "kind": "string"
    }
  },
  "wiki +node-get": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "node-token": {
      "kind": "string"
    },
    "obj-type": {
      "kind": "string"
    },
    "space-id": {
      "kind": "string"
    }
  },
  "wiki +node-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    },
    "parent-node-token": {
      "kind": "string"
    },
    "space-id": {
      "kind": "string"
    }
  },
  "wiki +space-create": {
    "as": {
      "kind": "string"
    },
    "description": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "name": {
      "kind": "string"
    }
  },
  "wiki +space-list": {
    "as": {
      "kind": "string"
    },
    "dry-run": {
      "kind": ""
    },
    "format": {
      "kind": "string"
    },
    "help": {
      "kind": ""
    },
    "jq": {
      "kind": "string"
    },
    "json": {
      "kind": ""
    },
    "page-all": {
      "kind": ""
    },
    "page-limit": {
      "kind": "int"
    },
    "page-size": {
      "kind": "int"
    },
    "page-token": {
      "kind": "string"
    }
  }
});

module.exports = { LARK_CLI_FLAG_SNAPSHOT, LARK_CLI_FLAG_SNAPSHOT_VERSION };
