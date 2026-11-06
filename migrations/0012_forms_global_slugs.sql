-- Public hosted form URLs use a global slug namespace.

DO $$
DECLARE
    duplicate_form record;
    candidate_slug text;
    collision_suffix integer;
BEGIN
    FOR duplicate_form IN
        SELECT id, slug, organization_id
        FROM (
            SELECT id, slug, organization_id,
                   row_number() OVER (PARTITION BY slug ORDER BY created_at, id) AS slug_position
            FROM forms
        ) ranked
        WHERE slug_position > 1
        ORDER BY slug, organization_id, id
    LOOP
        candidate_slug := duplicate_form.slug || '--org-' || replace(duplicate_form.organization_id::text, '-', '');
        collision_suffix := 0;
        WHILE EXISTS (SELECT 1 FROM forms WHERE slug = candidate_slug) LOOP
            collision_suffix := collision_suffix + 1;
            candidate_slug := duplicate_form.slug || '--org-' ||
                replace(duplicate_form.organization_id::text, '-', '') || '-' || collision_suffix;
        END LOOP;

        UPDATE forms SET slug = candidate_slug WHERE id = duplicate_form.id;
    END LOOP;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS forms_slug_global_idx ON forms (slug);
