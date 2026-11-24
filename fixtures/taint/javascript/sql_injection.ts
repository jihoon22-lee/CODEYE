import express, { Request, Response } from "express";
import { Pool } from "pg";

const app = express();
const pool = new Pool();

app.get("/user", async (req: Request, res: Response) => {
  const id: string = req.query.id as string;
  const result = await pool.query("SELECT * FROM users WHERE id = '" + id + "'"); // expect: codeye.javascript.sql-injection
  res.json(result.rows);
});

function findByName(name: string) {
  const sql = `SELECT * FROM users WHERE name = '${name}'`;
  return pool.query(sql); // expect: codeye.javascript.sql-injection
}

app.post("/search", async (req: Request, res: Response) => {
  res.json(await findByName(req.body.name));
});
