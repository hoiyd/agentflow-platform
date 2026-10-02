package store

// Runs inside the Workspace migration transaction. Only the reserved local
// identity is renamed; issuer/subject and historical JSON evidence stay intact.
// Defer FK checks during the coordinated rename, then restore their exact modes.
// Archive guards are paused only on reference-bearing tables locked by this
// transaction, never for ordinary requests or execution.
const superIdentityMigration = `DO $$
DECLARE f record; c record; constraints jsonb; guards jsonb;
BEGIN
 IF EXISTS(SELECT 1 FROM auth_users WHERE id='super' AND (issuer<>'agentflow:local' OR subject<>'local')) THEN
   RAISE EXCEPTION 'Reserved super identity belongs to another principal';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM auth_users WHERE id='user_local') THEN RETURN; END IF;
 IF EXISTS(SELECT 1 FROM auth_users WHERE id='super') OR
   EXISTS(SELECT 1 FROM auth_users WHERE id='user_local' AND (issuer<>'agentflow:local' OR subject<>'local')) THEN
   RAISE EXCEPTION 'Reserved local identity conflict; refusing to merge users';
 END IF;
 LOCK TABLE auth_users IN EXCLUSIVE MODE;
 FOR c IN SELECT table_schema,table_name,column_name FROM information_schema.columns
   WHERE table_schema=current_schema() AND column_name IN('user_id','owner_user_id') AND udt_name='text'
   ORDER BY table_name,column_name
 LOOP EXECUTE format('LOCK TABLE %I.%I IN EXCLUSIVE MODE',c.table_schema,c.table_name); END LOOP;

 SELECT COALESCE(jsonb_agg(jsonb_build_object('relation',conrelid::regclass::text,'name',conname,
   'is_deferrable',condeferrable,'initially_deferred',condeferred)),'[]'::jsonb) INTO constraints
 FROM pg_constraint WHERE contype='f' AND connamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema())
   AND confrelid IN('auth_users'::regclass,to_regclass('workspaces'))
   AND EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=conrelid AND a.attnum=ANY(conkey) AND a.attname IN('user_id','owner_user_id'));
 FOR f IN SELECT * FROM jsonb_to_recordset(constraints) AS x(relation text,name text,is_deferrable boolean,initially_deferred boolean)
 LOOP EXECUTE format('ALTER TABLE %s ALTER CONSTRAINT %I DEFERRABLE INITIALLY IMMEDIATE',f.relation,f.name); END LOOP;
 SET CONSTRAINTS ALL DEFERRED;

 SELECT COALESCE(jsonb_agg(jsonb_build_object('relation',tgrelid::regclass::text,'mode',tgenabled)),'[]'::jsonb) INTO guards
 FROM pg_trigger WHERE tgname='workspace_active_write' AND NOT tgisinternal
   AND EXISTS(SELECT 1 FROM information_schema.columns cols WHERE cols.table_schema=current_schema()
     AND to_regclass(format('%I.%I',cols.table_schema,cols.table_name))=tgrelid AND cols.column_name IN('user_id','owner_user_id'));
 FOR f IN SELECT * FROM jsonb_to_recordset(guards) AS x(relation text,mode text)
 LOOP EXECUTE format('ALTER TABLE %s DISABLE TRIGGER workspace_active_write',f.relation); END LOOP;

 UPDATE auth_users SET id='super',name='Super' WHERE id='user_local';
 FOR c IN SELECT table_schema,table_name,column_name FROM information_schema.columns
   WHERE table_schema=current_schema() AND column_name IN('user_id','owner_user_id') AND udt_name='text'
   ORDER BY table_name,column_name
 LOOP EXECUTE format('UPDATE %I.%I SET %I=$1 WHERE %I=$2',c.table_schema,c.table_name,c.column_name,c.column_name) USING 'super','user_local'; END LOOP;

 SET CONSTRAINTS ALL IMMEDIATE;
 FOR f IN SELECT * FROM jsonb_to_recordset(constraints) AS x(relation text,name text,is_deferrable boolean,initially_deferred boolean)
 LOOP EXECUTE format('ALTER TABLE %s ALTER CONSTRAINT %I %s',f.relation,f.name,
   CASE WHEN NOT f.is_deferrable THEN 'NOT DEFERRABLE' WHEN f.initially_deferred THEN 'DEFERRABLE INITIALLY DEFERRED' ELSE 'DEFERRABLE INITIALLY IMMEDIATE' END); END LOOP;
 FOR f IN SELECT * FROM jsonb_to_recordset(guards) AS x(relation text,mode text)
 LOOP EXECUTE format('ALTER TABLE %s %s TRIGGER workspace_active_write',f.relation,
   CASE f.mode WHEN 'D' THEN 'DISABLE' WHEN 'A' THEN 'ENABLE ALWAYS' WHEN 'R' THEN 'ENABLE REPLICA' ELSE 'ENABLE' END); END LOOP;
END $$`
