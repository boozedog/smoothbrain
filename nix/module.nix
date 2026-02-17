self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.smoothbrain;

  credPath = name: "/run/credentials/smoothbrain.service/${name}";

  pluginConfig =
    lib.optionalAttrs cfg.plugins.uptime-kuma.enable {
      uptime-kuma =
        lib.optionalAttrs (cfg.plugins.uptime-kuma.webhookTokenFile != null) {
          webhook_token_file = credPath "uptime-kuma-webhook-token";
        };
    }
    // lib.optionalAttrs cfg.plugins.xai.enable {
      xai = {
        model = cfg.plugins.xai.model;
      } // lib.optionalAttrs (cfg.plugins.xai.apiKeyFile != null) {
        api_key_file = credPath "xai-api-key";
      };
    }
    // lib.optionalAttrs cfg.plugins.mattermost.enable {
      mattermost = {
        url = cfg.plugins.mattermost.url;
      } // lib.optionalAttrs (cfg.plugins.mattermost.tokenFile != null) {
        token_file = credPath "mattermost-token";
      };
    };

  credentials =
    lib.optional (cfg.plugins.uptime-kuma.webhookTokenFile != null)
      "uptime-kuma-webhook-token:${cfg.plugins.uptime-kuma.webhookTokenFile}"
    ++ lib.optional (cfg.plugins.xai.apiKeyFile != null)
      "xai-api-key:${cfg.plugins.xai.apiKeyFile}"
    ++ lib.optional (cfg.plugins.mattermost.tokenFile != null)
      "mattermost-token:${cfg.plugins.mattermost.tokenFile}";

  routeSubmodule = lib.types.submodule {
    options = with lib; {
      name = mkOption { type = types.str; };
      source = mkOption { type = types.str; };
      event = mkOption { type = types.str; default = ""; };
      pipeline = mkOption {
        type = types.listOf (types.submodule {
          options = {
            plugin = mkOption { type = types.str; };
            action = mkOption { type = types.str; };
            params = mkOption { type = types.attrs; default = { }; };
          };
        });
        default = [ ];
      };
      sink = mkOption {
        type = types.submodule {
          options = {
            plugin = mkOption { type = types.str; };
            params = mkOption { type = types.attrs; default = { }; };
          };
        };
      };
    };
  };

  supervisorTaskSubmodule = lib.types.submodule {
    options = with lib; {
      name = mkOption { type = types.str; };
      schedule = mkOption { type = types.str; };
      prompt = mkOption { type = types.str; };
      plugin = mkOption { type = types.str; };
    };
  };

  configFile = (pkgs.formats.json { }).generate "config.json" {
    http.address = cfg.http.address;
    database = cfg.database;
    plugins = pluginConfig;
    routes = cfg.routes;
    supervisor.tasks = cfg.supervisor.tasks;
  };
in
{
  options.services.smoothbrain = with lib; {
    enable = mkEnableOption "smoothbrain orchestrator";

    http.address = mkOption {
      type = types.str;
      default = "127.0.0.1:8080";
      description = "HTTP listen address";
    };

    database = mkOption {
      type = types.str;
      default = "/var/lib/smoothbrain/state.db";
      description = "Path to SQLite database";
    };

    plugins = {
      uptime-kuma = {
        enable = mkEnableOption "Uptime Kuma plugin";
        webhookTokenFile = mkOption {
          type = types.nullOr types.str;
          default = null;
          description = "Path to file containing webhook authentication token";
        };
      };

      xai = {
        enable = mkEnableOption "xAI plugin";
        model = mkOption {
          type = types.str;
          default = "grok-3";
          description = "xAI model to use";
        };
        apiKeyFile = mkOption {
          type = types.nullOr types.str;
          default = null;
          description = "Path to file containing xAI API key";
        };
      };

      mattermost = {
        enable = mkEnableOption "Mattermost plugin";
        url = mkOption {
          type = types.nullOr types.str;
          default = null;
          description = "Mattermost server URL";
        };
        tokenFile = mkOption {
          type = types.nullOr types.str;
          default = null;
          description = "Path to file containing Mattermost token";
        };
      };
    };

    routes = mkOption {
      type = types.listOf routeSubmodule;
      default = [ ];
      description = "Event routing pipelines";
    };

    supervisor.tasks = mkOption {
      type = types.listOf supervisorTaskSubmodule;
      default = [ ];
      description = "Periodic LLM supervisor tasks";
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.smoothbrain = {
      isSystemUser = true;
      group = "smoothbrain";
    };
    users.groups.smoothbrain = { };

    systemd.services.smoothbrain = {
      description = "Smoothbrain orchestrator";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];

      serviceConfig = {
        ExecStart = "${self.packages.${pkgs.system}.default}/bin/smoothbrain -config ${configFile}";
        User = "smoothbrain";
        Group = "smoothbrain";
        StateDirectory = "smoothbrain";
        Restart = "on-failure";
        RestartSec = 5;

        # Hardening
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        ReadWritePaths = [ "/var/lib/smoothbrain" ];
      } // lib.optionalAttrs (credentials != [ ]) {
        LoadCredential = credentials;
      };
    };
  };
}
