-- 0010_forms.sql
-- Forms module: form definitions, fields, submissions, and list/tag associations.

CREATE TABLE IF NOT EXISTS forms (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    name            text NOT NULL,
    slug            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'published', 'unpublished')),
    list_id         uuid REFERENCES contact_lists(id) ON DELETE SET NULL,
    success_message text NOT NULL DEFAULT 'Thank you for subscribing!',
    redirect_url    text NOT NULL DEFAULT '',
    consent_text    text NOT NULL DEFAULT 'I agree to receive emails.',
    spam_protection boolean NOT NULL DEFAULT true,
    double_opt_in   boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug)
);

CREATE INDEX IF NOT EXISTS forms_org_idx ON forms (organization_id, status);
CREATE INDEX IF NOT EXISTS forms_slug_idx ON forms (slug);

CREATE TABLE IF NOT EXISTS form_fields (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    form_id         uuid NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    field_type      text NOT NULL
                    CHECK (field_type IN ('email', 'first_name', 'last_name', 'phone', 'company', 'country', 'custom')),
    label           text NOT NULL,
    placeholder     text NOT NULL DEFAULT '',
    required        boolean NOT NULL DEFAULT false,
    position        integer NOT NULL DEFAULT 0,
    custom_key      text,
    options         jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (form_id, position)
);

CREATE INDEX IF NOT EXISTS form_fields_form_idx ON form_fields (form_id, position);

CREATE TABLE IF NOT EXISTS form_submissions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    form_id         uuid NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_id      uuid REFERENCES contacts(id) ON DELETE SET NULL,
    data            jsonb NOT NULL DEFAULT '{}'::jsonb,
    consent         boolean NOT NULL DEFAULT false,
    ip_address      inet,
    user_agent      text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS form_submissions_form_idx ON form_submissions (form_id, created_at DESC);
CREATE INDEX IF NOT EXISTS form_submissions_org_idx ON form_submissions (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS form_tags (
    form_id         uuid NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    PRIMARY KEY (form_id, tag_id)
);

CREATE INDEX IF NOT EXISTS form_tags_form_idx ON form_tags (form_id);

DROP TRIGGER IF EXISTS forms_set_updated_at ON forms;
CREATE TRIGGER forms_set_updated_at
    BEFORE UPDATE ON forms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
