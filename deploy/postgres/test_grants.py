#!/usr/bin/env python3
"""Offline validation of executable grant plans; real SQL runs in store tests."""
import unittest
from unittest import mock
import json
import grants


class GrantPlans(unittest.TestCase):
    def test_identifier_and_role_combinations_are_closed(self):
        for schema, role, components in [
            ('public;DROP SCHEMA public', 'runtime', ['api']),
            ('public', 'r' * 64, ['api']),
            ('public', 'runtime', []),
            ('public', 'runtime', ['api', 'delivery']),
            ('public', 'runtime', ['operator', 'retention']),
            ('public', 'runtime', ['delivery', 'delivery']),
            ('public', 'runtime', ['unknown']),
        ]:
            with self.assertRaises(ValueError):
                grants.render(schema, role, components)

    def test_bundles_union_only_named_worker_grants(self):
        sql = grants.render('private', 'worker', ['ingestion', 'reports'])
        self.assertIn('ON TABLE "private"."dashboard_report_tasks"', sql)
        self.assertIn('ON TABLE "private"."dashboard_source_events"', sql)
        self.assertNotIn('ON TABLE "private"."dashboard_sessions" TO', sql)
        self.assertNotIn('GRANT UPDATE ("display_name")', sql)
        self.assertTrue(sql.endswith('COMMIT;\n'))

    def test_every_plan_revokes_stale_columns_without_modifying_public(self):
        for role in grants.COMPONENTS:
            sql = grants.render('private', 'worker', [role])
            self.assertIn('REVOKE ALL (%s)', sql)
            self.assertIn('pg_auth_members', sql)
            self.assertIn('rolbypassrls', sql)
            self.assertIn('public table or column privileges', sql)
            self.assertNotIn('FROM PUBLIC', sql)
            self.assertNotIn('ALTER DEFAULT PRIVILEGES', sql)
            self.assertNotIn('CREATE ROLE', sql)
            self.assertNotIn('PASSWORD', sql)
            self.assertNotIn('SECURITY DEFINER', sql)

    def test_wildcard_columns_are_rejected(self):
        manifest = {"roles": {"api": {"dashboard_accounts": {"select": "*"}}}}
        with mock.patch.object(grants.Path, "read_text", return_value=json.dumps(manifest)):
            with self.assertRaises(ValueError):
                grants.render("private", "api", ["api"])


if __name__ == '__main__':
    unittest.main()
