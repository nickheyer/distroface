INSERT INTO roles (name, description, permissions) VALUES 
('anonymous', 'Unauthenticated access', 
 '[
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"}
 ]'),

('reader', 'Basic read access', 
 '[
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"VIEW","resource":"USER"},
    {"action":"VIEW","resource":"GROUP"}
 ]'),

('developer', 'Standard developer access', 
 '[
    {"action":"MIGRATE","resource":"TASK"},
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"UPDATE","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"PUSH","resource":"IMAGE"},
    {"action":"VIEW","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"CREATE","resource":"TAG"},
    {"action":"DELETE","resource":"TAG"},
    {"action":"VIEW","resource":"USER"},
    {"action":"VIEW","resource":"GROUP"}
 ]'),

('administrator', 'Full system access', 
 '[
    {"action":"ADMIN","resource":"SYSTEM"},
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"VIEW","resource":"USER"},
    {"action":"CREATE","resource":"USER"},
    {"action":"UPDATE","resource":"USER"},
    {"action":"DELETE","resource":"USER"},
    {"action":"VIEW","resource":"GROUP"},
    {"action":"CREATE","resource":"GROUP"},
    {"action":"UPDATE","resource":"GROUP"},
    {"action":"DELETE","resource":"GROUP"},
    {"action":"UPDATE","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"PUSH","resource":"IMAGE"},
    {"action":"MIGRATE","resource":"TASK"},
    {"action":"DELETE","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"CREATE","resource":"TAG"},
    {"action":"DELETE","resource":"TAG"}
 ]');

-- Insert default groups
INSERT INTO groups (name, description, roles, scope) VALUES 
('admins', 'System Administrators', '["administrator"]', 'system:all'),
('developers', 'Development Team', '["developer"]', 'system:all'),
('readers', 'Read-only Users', '["reader"]', 'system:all');


-- Create default admin user with password 'admin'
INSERT INTO users (username, password, groups) VALUES 
('admin', '$2b$12$t.owjcZ9NU85Ikgxo/4gMu6zBOAo608pmYeKOlOuUb6RMjgjKgXXa', '["admins"]');
