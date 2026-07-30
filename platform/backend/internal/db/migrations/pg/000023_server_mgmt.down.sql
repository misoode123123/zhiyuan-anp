DROP TABLE IF EXISTS appdeploy_server_metric;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS last_seen;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS winrm_password;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS winrm_user;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS ssh_key;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS ssh_port;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS connect_type;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS env;
ALTER TABLE deploy_node DROP COLUMN IF EXISTS os_type;
