-- +goose Up
CREATE TABLE achievements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    reward_coins INT NOT NULL DEFAULT 0,
    condition_type TEXT NOT NULL,
    condition_value INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_achievements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_id UUID NOT NULL REFERENCES achievements(id) ON DELETE CASCADE,
    completed_at TIMESTAMPTZ,
    reward_claimed_at TIMESTAMPTZ,
    progress INT NOT NULL DEFAULT 0,
    UNIQUE(user_id, achievement_id)
);

INSERT INTO achievements (name, description, reward_coins, condition_type, condition_value) VALUES
    ('Новичок',        'Сыграйте 3 матча',         50,  'matches_played', 3),
    ('Опытный стратег','Сыграйте 10 матчей',       500, 'matches_played', 10),
    ('Охотник',        'Уничтожьте 3 корабля',     67,  'ships_killed',   3);

-- +goose Down
DROP TABLE IF EXISTS user_achievements;
DROP TABLE IF EXISTS achievements;
