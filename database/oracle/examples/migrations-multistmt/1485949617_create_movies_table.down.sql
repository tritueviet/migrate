BEGIN
EXECUTE IMMEDIATE 'DROP TABLE MOVIES_MS';
EXCEPTION
    WHEN OTHERS THEN
        IF SQLCODE != -942 THEN
            RAISE;
        END IF;
END;
