import express, { Request, Response } from "express";
import { Pool } from "pg";

const app = express();
const pool = new Pool();

app.get("/user", async (req: Request, res: Response) => {
  const result = await pool.query("SELECT * FROM users WHERE id = $1", [req.query.id]);
  res.json(result.rows);
});

app.get("/page", async (req: Request, res: Response) => {
  const limit = Number(req.query.limit);
  const result = await pool.query("SELECT * FROM users LIMIT " + limit);
  res.json(result.rows);
});

app.post("/named", async (req: Request, res: Response) => {
  const result = await pool.query({ text: "SELECT * FROM users WHERE name = $1", values: [req.body.name] });
  res.json(result.rows);
});
