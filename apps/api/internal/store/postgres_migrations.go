package store

import "context"

func (s *PostgresStore) migrate(ctx context.Context) error {
	for _, statement := range postgresMigrations {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// Preserve upgrade order across domain files: later statements depend on earlier tables.
var postgresMigrations = func() []string {
	var statements []string
	for _, group := range [][]string{
		postgresIdentitySchema,
		postgresConversationSchema,
		postgresExecutionSchema,
		postgresContextSchema,
		postgresMemorySchema,
		postgresDocumentSchema,
		postgresRuntimeIndexes,
		postgresUsageSchema,
		postgresTaskStateSchema,
		postgresLookupIndexes,
		postgresChildRunRetirement,
	} {
		statements = append(statements, group...)
	}
	return statements
}()
