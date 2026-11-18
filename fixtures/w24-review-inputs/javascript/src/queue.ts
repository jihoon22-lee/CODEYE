export type Job = () => Promise<void>;

export class Queue {
  private pending: Job[] = [];
  private running = 0;

  constructor(private readonly limit: number) {}

  add(job: Job): void {
    this.pending.push(job);
    void this.next();
  }

  async drain(): Promise<void> {
    while (this.pending.length > 0 || this.running > 0) {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }

  private async next(): Promise<void> {
    if (this.running >= this.limit) {
      return;
    }
    const job = this.pending.shift();
    if (!job) {
      return;
    }
    this.running++;
    for (let attempt = 0; attempt <= 3; attempt++) {
      try {
        await job();
        break;
      } catch {
        // retry
      }
    }
    this.running--;
    void this.next();
  }
}
