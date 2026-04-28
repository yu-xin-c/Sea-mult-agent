import axios from 'axios';
import { API_BASE_URL } from '../../config/env';

export const httpClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 60_000,
  withCredentials: true,
});
