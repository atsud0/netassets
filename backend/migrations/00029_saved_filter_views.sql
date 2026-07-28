-- +goose Up
CREATE TABLE saved_filter_views (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    page_key varchar(64) NOT NULL,
    name varchar(100) NOT NULL,
    filters jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT saved_filter_views_filters_object_check CHECK (jsonb_typeof(filters) = 'object'),
    CONSTRAINT saved_filter_views_user_page_name_unique UNIQUE (user_id, page_key, name)
);

CREATE INDEX saved_filter_views_user_page_updated_idx
    ON saved_filter_views(user_id, page_key, updated_at DESC);
CREATE UNIQUE INDEX saved_filter_views_user_page_default_idx
    ON saved_filter_views(user_id, page_key) WHERE is_default;

-- +goose Down
DROP TABLE IF EXISTS saved_filter_views;
