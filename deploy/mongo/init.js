// Local Mongo bootstrap: create the app db, a least-privilege user, and a
// smoke collection. Runs once on an empty data volume.
db = db.getSiblingDB('air');

db.createUser({
  user: 'air_app',
  pwd: 'localdevpass', // local-dev placeholder only
  roles: [{ role: 'readWrite', db: 'air' }],
});

db.createCollection('healthcheck');
db.healthcheck.insertOne({ checked_at: new Date() });
